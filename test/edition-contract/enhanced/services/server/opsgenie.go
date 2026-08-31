package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	opsgenieProviderName       = "opsgenie"
	opsgenieUSURL              = "https://api.opsgenie.com"
	opsgenieEUURL              = "https://api.eu.opsgenie.com"
	opsgenieRequestTimeout     = 10 * time.Second
	opsgenieMaxResponseBytes   = 8 * 1024
	opsgenieMaxCredentialBytes = 256
	opsgenieMaxRequestIDBytes  = 128
	opsgenieMaxRetryAfter      = 24 * time.Hour
)

type opsgenieAdapter struct {
	client *http.Client
	now    func() time.Time
}

type opsgenieAlertPayload struct {
	Message     string                                         `json:"message"`
	Alias       string                                         `json:"alias"`
	Description string                                         `json:"description"`
	Responders  []pro_interfaces.NotificationOpsgenieResponder `json:"responders,omitempty"`
	Tags        []string                                       `json:"tags,omitempty"`
	Details     opsgenieAlertDetails                           `json:"details"`
	Entity      string                                         `json:"entity"`
	Source      string                                         `json:"source"`
	Priority    string                                         `json:"priority"`
}

type opsgenieAlertDetails struct {
	EventID         string `json:"event_id"`
	Source          string `json:"source"`
	Lifecycle       string `json:"lifecycle"`
	Severity        string `json:"severity"`
	LifecycleAction string `json:"lifecycle_action"`
}

// NewOpsgenieAdapter pins production egress to the two documented API hosts.
func NewOpsgenieAdapter() pro_interfaces.NotificationProviderAdapter {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newOpsgenieAdapter(&http.Client{Transport: transport}, time.Now)
}

func NewOpsgenieAdapterWithHTTPClient(client *http.Client) pro_interfaces.NotificationProviderAdapter {
	return newOpsgenieAdapter(client, time.Now)
}
func newOpsgenieAdapter(client *http.Client, now func() time.Time) *opsgenieAdapter {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if now == nil {
		now = time.Now
	}
	return &opsgenieAdapter{client: &copy, now: now}
}
func (*opsgenieAdapter) ProviderName() string { return opsgenieProviderName }

func (a *opsgenieAdapter) Dispatch(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	if request.ProviderRequestID != "" {
		return a.poll(ctx, request)
	}
	endpoint, ok := opsgenieEndpoint(request.Region)
	if !ok || !validOpsgenieKey(string(request.Credential)) {
		return opsgeniePermanent()
	}
	method, suffix, body, ok := opsgenieOperation(request)
	if !ok {
		return opsgeniePermanent()
	}
	return a.do(ctx, method, endpoint+suffix, body, request.Credential)
}

func (a *opsgenieAdapter) do(ctx context.Context, method, endpoint string, body []byte, credential []byte) pro_interfaces.NotificationDispatchResult {
	requestContext, cancel := context.WithTimeout(ctx, opsgenieRequestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return opsgeniePermanent()
	}
	httpRequest.Header.Set("Authorization", "GenieKey "+string(credential))
	if len(body) > 0 {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	response, err := a.client.Do(httpRequest)
	if err != nil || response == nil {
		return opsgenieTransient(0)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, opsgenieMaxResponseBytes+1))
	if len(payload) > opsgenieMaxResponseBytes {
		return opsgeniePermanent()
	}
	if response.StatusCode == http.StatusAccepted {
		requestID, ok := opsgenieRequestID(payload)
		if !ok {
			return opsgeniePermanent()
		}
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchPending, RequestID: requestID}
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchRateLimited, RetryAfter: opsgenieRetryAfter(response.Header, a.now())}
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode >= 500 {
		return opsgenieTransient(0)
	}
	if method == http.MethodPost && strings.Contains(endpoint, "/close?") && response.StatusCode == http.StatusNotFound {
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded}
	}
	return opsgeniePermanent()
}

func (a *opsgenieAdapter) poll(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	endpoint, ok := opsgenieEndpoint(request.Region)
	if !ok || !validOpsgenieKey(string(request.Credential)) || !pro_interfaces.ValidNotificationProviderRequestID(request.ProviderRequestID) {
		return opsgeniePermanent()
	}
	requestContext, cancel := context.WithTimeout(ctx, opsgenieRequestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodGet, endpoint+"/v2/alerts/requests/"+url.PathEscape(request.ProviderRequestID), nil)
	if err != nil {
		return opsgeniePermanent()
	}
	httpRequest.Header.Set("Authorization", "GenieKey "+string(request.Credential))
	response, err := a.client.Do(httpRequest)
	if err != nil || response == nil {
		return opsgenieTransient(0)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, opsgenieMaxResponseBytes+1))
	if len(payload) > opsgenieMaxResponseBytes {
		return opsgeniePermanent()
	}
	switch response.StatusCode {
	case http.StatusOK:
		success, known, idempotent := opsgenieRequestSuccess(payload)
		if !known {
			return opsgeniePermanent()
		}
		if success || request.Event.LifecycleAction == pro_interfaces.NotificationLifecycleResolve && idempotent {
			return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded}
		}
		return opsgeniePermanent()
	case http.StatusAccepted, http.StatusNotFound:
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchPending, RequestID: request.ProviderRequestID}
	case http.StatusTooManyRequests:
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchRateLimited, RetryAfter: opsgenieRetryAfter(response.Header, a.now())}
	case http.StatusRequestTimeout, http.StatusTooEarly:
		return opsgenieTransient(0)
	default:
		if response.StatusCode >= 500 {
			return opsgenieTransient(0)
		}
		return opsgeniePermanent()
	}
}

func opsgenieOperation(request pro_interfaces.NotificationDispatchRequest) (string, string, []byte, bool) {
	if request.Provider != opsgenieProviderName || request.IncidentKey == "" || len(request.IncidentKey) > 512 || request.Event.IncidentKey != request.IncidentKey || request.Event.Details.Message != "" {
		return "", "", nil, false
	}
	if request.Event.LifecycleAction == pro_interfaces.NotificationLifecycleResolve {
		return http.MethodPost, "/v2/alerts/" + url.PathEscape(request.IncidentKey) + "/close?identifierType=alias", []byte(`{}`), true
	}
	if request.Event.LifecycleAction != pro_interfaces.NotificationLifecycleTrigger && request.Event.LifecycleAction != pro_interfaces.NotificationLifecycleUpdate {
		return "", "", nil, false
	}
	payload, ok := opsgenieCreatePayload(request)
	if !ok {
		return "", "", nil, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > 32*1024 {
		return "", "", nil, false
	}
	return http.MethodPost, "/v2/alerts", encoded, true
}

func opsgenieCreatePayload(request pro_interfaces.NotificationDispatchRequest) (opsgenieAlertPayload, bool) {
	message := opsgenieBounded("Semaphore "+string(request.Event.Severity)+" "+string(request.Event.Source.Kind), 130)
	description := opsgenieBounded("incident="+request.IncidentKey+" source="+request.Event.Source.ID+" lifecycle="+request.Event.LifecycleID, 15000)
	payload := opsgenieAlertPayload{Message: message, Alias: request.IncidentKey, Description: description,
		Tags: opsgenieTags(request.Event), Details: opsgenieAlertDetails{EventID: opsgenieBounded(request.Event.EventID, 64), Source: opsgenieBounded(request.Event.Source.ID, 100), Lifecycle: opsgenieBounded(request.Event.LifecycleID, 512), Severity: opsgenieBounded(string(request.Event.Severity), 50), LifecycleAction: opsgenieBounded(string(request.Event.LifecycleAction), 50)},
		Entity: opsgenieBounded(request.Event.LifecycleID, 512), Source: opsgenieBounded(request.Event.Source.ID, 100)}
	priority := opsgeniePriority(request.Event.Severity)
	if request.Opsgenie != nil && request.Opsgenie.Priority != "" {
		priority = string(request.Opsgenie.Priority)
	}
	payload.Priority = priority
	if request.Opsgenie != nil && len(request.Opsgenie.Responders) > 0 {
		payload.Responders = request.Opsgenie.Responders
	}
	return payload, true
}

func opsgenieTags(event pro_interfaces.NotificationEvent) []string {
	values := []string{"semaphore", string(event.Source.Kind), string(event.Severity), string(event.LifecycleAction)}
	if event.ProjectID == nil {
		values = append(values, "global")
	} else {
		values = append(values, "project")
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if len(result) == 20 {
			break
		}
		result = append(result, opsgenieBounded(value, 50))
	}
	return result
}

func opsgenieEndpoint(region pro_interfaces.NotificationProviderRegion) (string, bool) {
	if region == pro_interfaces.NotificationProviderRegionUS {
		return opsgenieUSURL, true
	}
	if region == pro_interfaces.NotificationProviderRegionEU {
		return opsgenieEUURL, true
	}
	return "", false
}
func opsgeniePriority(severity pro_interfaces.NotificationSeverity) string {
	switch severity {
	case pro_interfaces.NotificationSeverityCritical:
		return "P1"
	case pro_interfaces.NotificationSeverityError:
		return "P2"
	case pro_interfaces.NotificationSeverityWarning:
		return "P3"
	default:
		return "P5"
	}
}
func opsgenieBounded(value string, maximum int) string {
	value = strings.ToValidUTF8(value, "")
	for len(value) > maximum {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}
func opsgenieTransient(after time.Duration) pro_interfaces.NotificationDispatchResult {
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient, RetryAfter: after}
}
func opsgeniePermanent() pro_interfaces.NotificationDispatchResult {
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchPermanent}
}

func opsgenieRequestID(body []byte) (string, bool) {
	var response struct {
		RequestID string `json:"requestId"`
	}
	if json.Unmarshal(body, &response) != nil || !pro_interfaces.ValidNotificationProviderRequestID(response.RequestID) {
		return "", false
	}
	return response.RequestID, true
}
func opsgenieRequestSuccess(body []byte) (bool, bool, bool) {
	var response struct {
		Data struct {
			IsSuccess *bool  `json:"isSuccess"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &response) != nil || response.Data.IsSuccess == nil {
		return false, false, false
	}
	return *response.Data.IsSuccess, true, response.Data.Status == "Alert does not exist"
}
func opsgenieRetryAfter(header http.Header, now time.Time) time.Duration {
	for _, value := range []string{header.Get("X-RateLimit-Period-In-Sec"), header.Get("Retry-After")} {
		seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || seconds <= 0 {
			continue
		}
		if seconds > int64(opsgenieMaxRetryAfter/time.Second) {
			return opsgenieMaxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	return 0
}
