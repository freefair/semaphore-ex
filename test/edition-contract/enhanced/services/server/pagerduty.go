package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	pagerDutyUSURL             = "https://events.pagerduty.com/v2/enqueue"
	pagerDutyEUURL             = "https://events.eu.pagerduty.com/v2/enqueue"
	pagerDutyRequestTimeout    = 10 * time.Second
	pagerDutyMaxResponseBytes  = 8 * 1024
	pagerDutyMaxPayloadBytes   = 512 * 1024
	pagerDutyMaxRetryAfter     = 24 * time.Hour
	pagerDutyMaxDedupKeyBytes  = 255
	pagerDutyMaxSummaryBytes   = 1024
	pagerDutyMaxSourceBytes    = 1024
	pagerDutyMaxComponentBytes = 255
	pagerDutyMaxGroupBytes     = 255
	pagerDutyMaxClassBytes     = 255
)

var pagerDutyRoutingKeyPattern = regexp.MustCompile(`^[A-Za-z0-9]{32}$`)

// pagerDutyAdapter is intentionally constrained to the documented regional
// Events API endpoints. It has no endpoint, proxy, or response-body escape
// hatch in the destination contract.
type pagerDutyAdapter struct {
	client *http.Client
	now    func() time.Time
}

type pagerDutyEvent struct {
	RoutingKey  string                `json:"routing_key"`
	EventAction string                `json:"event_action"`
	DedupKey    string                `json:"dedup_key"`
	Payload     pagerDutyEventPayload `json:"payload"`
}

type pagerDutyEventPayload struct {
	Summary       string         `json:"summary"`
	Source        string         `json:"source"`
	Severity      string         `json:"severity"`
	Timestamp     string         `json:"timestamp"`
	Component     string         `json:"component,omitempty"`
	Group         string         `json:"group,omitempty"`
	Class         string         `json:"class,omitempty"`
	CustomDetails map[string]any `json:"custom_details,omitempty"`
}

// NewPagerDutyAdapter creates the only outbound provider adapter in the
// Enhanced graph. Its transport bypasses environment proxies and rejects every
// redirect, keeping production egress pinned to the selected fixed endpoint.
func NewPagerDutyAdapter() pro_interfaces.NotificationProviderAdapter {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newPagerDutyAdapter(&http.Client{Transport: transport}, time.Now)
}

// NewPagerDutyAdapterWithHTTPClient exists for deterministic tests and
// code-owned transport instrumentation. It still refuses redirects and never
// accepts an endpoint from a destination or request.
func NewPagerDutyAdapterWithHTTPClient(client *http.Client) pro_interfaces.NotificationProviderAdapter {
	return newPagerDutyAdapter(client, time.Now)
}

func newPagerDutyAdapter(client *http.Client, now func() time.Time) *pagerDutyAdapter {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if now == nil {
		now = time.Now
	}
	return &pagerDutyAdapter{client: &copy, now: now}
}

func (*pagerDutyAdapter) ProviderName() string { return pagerDutyProviderName }

func (a *pagerDutyAdapter) Dispatch(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	body, endpoint, valid := pagerDutyRequestBody(request)
	if !valid {
		return pagerDutyPermanentResult()
	}
	requestContext, cancel := context.WithTimeout(ctx, pagerDutyRequestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return pagerDutyPermanentResult()
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(httpRequest)
	if err != nil || response == nil {
		return pagerDutyTransientResult(0)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, pagerDutyMaxResponseBytes+1))

	retryAfter := pagerDutyRetryAfter(response.Header.Get("Retry-After"), a.now())
	switch {
	case response.StatusCode == http.StatusAccepted:
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded}
	case response.StatusCode == http.StatusTooManyRequests:
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchRateLimited, RetryAfter: retryAfter}
	case response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooEarly || response.StatusCode >= http.StatusInternalServerError:
		return pagerDutyTransientResult(retryAfter)
	default:
		return pagerDutyPermanentResult()
	}
}

func pagerDutyRequestBody(request pro_interfaces.NotificationDispatchRequest) ([]byte, string, bool) {
	if request.Provider != pagerDutyProviderName || !pagerDutyRoutingKeyPattern.Match(request.Credential) ||
		request.IncidentKey == "" || len(request.IncidentKey) > pagerDutyMaxDedupKeyBytes || request.Event.IncidentKey != request.IncidentKey ||
		request.Event.Details.Message != "" {
		return nil, "", false
	}
	endpoint, valid := pagerDutyEndpoint(request.Region)
	if !valid {
		return nil, "", false
	}
	action, valid := pagerDutyAction(request.Event.LifecycleAction)
	if !valid {
		return nil, "", false
	}
	payload := pagerDutyEvent{
		RoutingKey:  string(request.Credential),
		EventAction: action,
		DedupKey:    request.IncidentKey,
		Payload: pagerDutyEventPayload{
			Summary:       pagerDutySummary(request.Event, action),
			Source:        pagerDutyBoundedString(request.Event.Source.ID, pagerDutyMaxSourceBytes),
			Severity:      pagerDutySeverity(request.Event.Severity),
			Timestamp:     request.Event.OccurredAt.UTC().Format(time.RFC3339),
			Component:     pagerDutyBoundedString(string(request.Event.Source.Kind), pagerDutyMaxComponentBytes),
			Group:         pagerDutyEventGroup(request.Event),
			Class:         pagerDutyBoundedString(request.Event.LifecycleID, pagerDutyMaxClassBytes),
			CustomDetails: pagerDutyCustomDetails(request.Event),
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > pagerDutyMaxPayloadBytes {
		return nil, "", false
	}
	return encoded, endpoint, true
}

func pagerDutyEndpoint(region pro_interfaces.NotificationProviderRegion) (string, bool) {
	switch region {
	case pro_interfaces.NotificationProviderRegionUS:
		return pagerDutyUSURL, true
	case pro_interfaces.NotificationProviderRegionEU:
		return pagerDutyEUURL, true
	default:
		return "", false
	}
}

func pagerDutyAction(action pro_interfaces.NotificationLifecycleAction) (string, bool) {
	switch action {
	case pro_interfaces.NotificationLifecycleTrigger, pro_interfaces.NotificationLifecycleUpdate:
		return "trigger", true
	case pro_interfaces.NotificationLifecycleResolve:
		return "resolve", true
	default:
		return "", false
	}
}

func pagerDutySeverity(severity pro_interfaces.NotificationSeverity) string {
	switch severity {
	case pro_interfaces.NotificationSeverityCritical:
		return "critical"
	case pro_interfaces.NotificationSeverityError:
		return "error"
	case pro_interfaces.NotificationSeverityWarning:
		return "warning"
	default:
		return "info"
	}
}

func pagerDutySummary(event pro_interfaces.NotificationEvent, action string) string {
	if event.Source.Kind == pro_interfaces.NotificationSourceSystem && strings.HasPrefix(event.Source.ID, "system:notification_test-") {
		return "Semaphore notification test"
	}
	return pagerDutyBoundedString("Semaphore "+pagerDutySeverity(event.Severity)+" "+string(event.Source.Kind)+" "+action, pagerDutyMaxSummaryBytes)
}

func pagerDutyEventGroup(event pro_interfaces.NotificationEvent) string {
	if event.ProjectID == nil {
		return "global"
	}
	return pagerDutyBoundedString("project:"+strconv.Itoa(*event.ProjectID), pagerDutyMaxGroupBytes)
}

func pagerDutyCustomDetails(event pro_interfaces.NotificationEvent) map[string]any {
	details := map[string]any{
		"schema_version":   pagerDutyBoundedString(event.SchemaVersion, 64),
		"event_id":         pagerDutyBoundedString(event.EventID, 64),
		"source_event_key": pagerDutyBoundedString(event.SourceEventKey, 128),
		"source_revision":  event.SourceRevision,
		"scope":            pagerDutyBoundedString(string(event.Scope), 32),
		"source_kind":      pagerDutyBoundedString(string(event.Source.Kind), 32),
		"source_id":        pagerDutyBoundedString(event.Source.ID, 256),
		"lifecycle_id":     pagerDutyBoundedString(event.LifecycleID, 256),
		"severity":         pagerDutyBoundedString(string(event.Severity), 16),
		"lifecycle_action": pagerDutyBoundedString(string(event.LifecycleAction), 16),
		"incident_key":     pagerDutyBoundedString(event.IncidentKey, pagerDutyMaxDedupKeyBytes),
		"occurred_at":      event.OccurredAt.UTC().Format(time.RFC3339),
	}
	if event.ProjectID != nil {
		details["project_id"] = *event.ProjectID
	}
	if event.Details.TaskID != nil {
		details["task_id"] = *event.Details.TaskID
	}
	if event.Details.TemplateID != nil {
		details["template_id"] = *event.Details.TemplateID
	}
	if event.Details.WorkflowID != nil {
		details["workflow_id"] = *event.Details.WorkflowID
	}
	if event.Details.WorkflowRunID != nil {
		details["workflow_run_id"] = *event.Details.WorkflowRunID
	}
	if event.Details.ApprovalID != nil {
		details["approval_id"] = *event.Details.ApprovalID
	}
	if event.Details.Status != "" {
		details["status"] = pagerDutyBoundedString(event.Details.Status, 256)
	}
	if event.Details.Decision != "" {
		details["decision"] = pagerDutyBoundedString(event.Details.Decision, 256)
	}
	return details
}

func pagerDutyBoundedString(value string, limit int) string {
	value = strings.ToValidUTF8(value, "")
	if len(value) <= limit {
		return value
	}
	for len(value) > limit {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}

func pagerDutyRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		if seconds > int64(pagerDutyMaxRetryAfter/time.Second) {
			return pagerDutyMaxRetryAfter
		}
		return pagerDutyBoundRetryAfter(time.Duration(seconds) * time.Second)
	}
	if when, err := http.ParseTime(value); err == nil {
		return pagerDutyBoundRetryAfter(when.Sub(now))
	}
	return 0
}

func pagerDutyBoundRetryAfter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	if delay > pagerDutyMaxRetryAfter {
		return pagerDutyMaxRetryAfter
	}
	return delay
}

func pagerDutyTransientResult(retryAfter time.Duration) pro_interfaces.NotificationDispatchResult {
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient, RetryAfter: retryAfter}
}

func pagerDutyPermanentResult() pro_interfaces.NotificationDispatchResult {
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchPermanent}
}
