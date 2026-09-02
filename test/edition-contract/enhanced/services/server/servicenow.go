package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	serviceNowRequestTimeout = 10 * time.Second
	serviceNowResponseLimit  = 8 * 1024
)

var serviceNowSysID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var serviceNowIncidentKey = regexp.MustCompile(`^[a-f0-9]{64}$`)

type serviceNowToken struct {
	value   string
	expires time.Time
}

// serviceNowAdapter owns HTTP only. The dispatcher owns the durable binding
// transition immediately before it asks this adapter to create a record.
type serviceNowAdapter struct {
	client *http.Client
	mu     sync.Mutex
	tokens map[string]serviceNowToken
}

type serviceNowLookupIP func(context.Context, string) ([]net.IPAddr, error)
type serviceNowDial func(context.Context, string, string) (net.Conn, error)

func NewServiceNowAdapter() pro_interfaces.NotificationProviderAdapter {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = newServiceNowDialContext(net.DefaultResolver.LookupIPAddr, (&net.Dialer{}).DialContext)
	return newServiceNowAdapter(&http.Client{Transport: transport})
}

func NewServiceNowAdapterWithHTTPClient(client *http.Client) pro_interfaces.NotificationProviderAdapter {
	return newServiceNowAdapter(client)
}

func newServiceNowAdapter(client *http.Client) *serviceNowAdapter {
	if client == nil {
		client = &http.Client{}
	}
	copy := *client
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &serviceNowAdapter{client: &copy, tokens: make(map[string]serviceNowToken)}
}

func (*serviceNowAdapter) ProviderName() string { return serviceNowProviderName }

func (a *serviceNowAdapter) Dispatch(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	if request.Provider != serviceNowProviderName || request.ServiceNow == nil || !validServiceNowConfiguration(request.ServiceNow) || !serviceNowIncidentKey.MatchString(request.IncidentKey) {
		return serviceNowPermanent()
	}
	if request.ProviderRecordID != "" {
		return a.update(ctx, request)
	}
	return a.create(ctx, request)
}

func validServiceNowOrigin(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.Hostname() == "" || net.ParseIP(parsed.Hostname()) != nil {
		return false
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return validServiceNowDNSName(host) && serviceNowVendorHost(host)
}

func validServiceNowDNSName(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				return false
			}
		}
	}
	return true
}

func serviceNowVendorHost(host string) bool {
	return strings.HasSuffix(host, ".service-now.com") && host != "service-now.com" || strings.HasSuffix(host, ".servicenow.com") && host != "servicenow.com"
}

func canonicalServiceNowOrigin(value string) string {
	parsed, _ := url.Parse(value)
	return "https://" + strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}

func newServiceNowDialContext(lookup serviceNowLookupIP, dial serviceNowDial) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || !serviceNowVendorHost(strings.ToLower(strings.TrimSuffix(host, "."))) {
			return nil, &net.DNSError{Err: "unapproved ServiceNow origin", Name: host}
		}
		addresses, err := lookup(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, err
		}
		for _, candidate := range addresses {
			if !serviceNowPublicIP(candidate.IP) {
				return nil, &net.DNSError{Err: "non-public ServiceNow address", Name: host}
			}
		}
		return dial(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}
}

func serviceNowPublicIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() {
		return false
	}
	for _, denied := range serviceNowNonPublicPrefixes {
		if denied.Contains(address) {
			return false
		}
	}
	return true
}

var serviceNowNonPublicPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("100::/64"), netip.MustParsePrefix("2001:2::/48"), netip.MustParsePrefix("2001:10::/28"),
	netip.MustParsePrefix("2001:20::/28"), netip.MustParsePrefix("2001:db8::/32"),
}

func (a *serviceNowAdapter) create(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	body, ok := serviceNowPayload(request)
	if !ok {
		return serviceNowPermanent()
	}
	response, outcome := a.do(ctx, request, http.MethodPost, "/api/now/v1/table/incident", body)
	if outcome.Outcome != "" {
		return outcome
	}
	if response.StatusCode != http.StatusCreated {
		response.Body.Close()
		if response.StatusCode == http.StatusConflict {
			return serviceNowAmbiguous()
		}
		return serviceNowStatus(response.StatusCode, response.Header.Get("Retry-After"), time.Now())
	}
	id, ok := serviceNowResponseID(response)
	if !ok {
		return serviceNowAmbiguous()
	}
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded, RecordID: id}
}

func (a *serviceNowAdapter) update(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	if !serviceNowSysID.MatchString(request.ProviderRecordID) {
		return serviceNowPermanent()
	}
	body, ok := serviceNowPayload(request)
	if !ok {
		return serviceNowPermanent()
	}
	delete(body, "correlation_id")
	response, outcome := a.do(ctx, request, http.MethodPatch, "/api/now/v1/table/incident/"+request.ProviderRecordID, body)
	if outcome.Outcome != "" {
		return outcome
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		response.Body.Close()
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded, RecordID: request.ProviderRecordID}
	}
	response.Body.Close()
	return serviceNowStatus(response.StatusCode, response.Header.Get("Retry-After"), time.Now())
}

// reconcile performs the only permitted lookup: an exact, bounded correlation
// query. It never returns a provider response body to the dispatcher.
func (a *serviceNowAdapter) ReconcileLifecycle(ctx context.Context, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationLifecycleReconciliation {
	if request.ServiceNow == nil || !serviceNowIncidentKey.MatchString(request.IncidentKey) {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: serviceNowPermanent()}
	}
	query := url.Values{"sysparm_query": {"correlation_id=" + request.IncidentKey}, "sysparm_limit": {"2"}, "sysparm_fields": {"sys_id,correlation_id"}}
	requestContext, cancel := context.WithTimeout(ctx, serviceNowRequestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, http.MethodGet, canonicalServiceNowOrigin(request.ServiceNow.InstanceOrigin)+"/api/now/v1/table/incident?"+query.Encode(), nil)
	if err != nil {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: serviceNowPermanent()}
	}
	if authorization := a.authorize(ctx, httpRequest, request); authorization.Outcome != "" {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: authorization}
	}
	response, err := a.client.Do(httpRequest)
	if err != nil || response == nil {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient}}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: serviceNowStatus(response.StatusCode, response.Header.Get("Retry-After"), time.Now())}
	}
	var payload struct {
		Result []struct {
			SysID         string `json:"sys_id"`
			CorrelationID string `json:"correlation_id"`
		} `json:"result"`
	}
	if !readServiceNowJSON(response.Body, &payload) {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: serviceNowPermanent()}
	}
	if len(payload.Result) == 0 {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded}}
	}
	if len(payload.Result) != 1 || !serviceNowSysID.MatchString(payload.Result[0].SysID) || payload.Result[0].CorrelationID != request.IncidentKey {
		return pro_interfaces.NotificationLifecycleReconciliation{Result: serviceNowPermanent()}
	}
	return pro_interfaces.NotificationLifecycleReconciliation{Found: true, RecordID: payload.Result[0].SysID, Result: pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchSucceeded}}
}

func (a *serviceNowAdapter) do(ctx context.Context, request pro_interfaces.NotificationDispatchRequest, method, path string, payload map[string]string) (*http.Response, pro_interfaces.NotificationDispatchResult) {
	origin := canonicalServiceNowOrigin(request.ServiceNow.InstanceOrigin)
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > 16*1024 {
		return nil, serviceNowPermanent()
	}
	requestContext, cancel := context.WithTimeout(ctx, serviceNowRequestTimeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestContext, method, origin+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, serviceNowPermanent()
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Content-Type", "application/json")
	if authorization := a.authorize(ctx, httpRequest, request); authorization.Outcome != "" {
		return nil, authorization
	}
	response, err := a.client.Do(httpRequest)
	if err != nil || response == nil {
		return nil, pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient}
	}
	if response.Request == nil {
		response.Request = httpRequest
	}
	return response, pro_interfaces.NotificationDispatchResult{}
}

func (a *serviceNowAdapter) authorize(ctx context.Context, httpRequest *http.Request, request pro_interfaces.NotificationDispatchRequest) pro_interfaces.NotificationDispatchResult {
	configuration := request.ServiceNow
	if configuration.AuthMode == pro_interfaces.NotificationServiceNowAuthBasic {
		httpRequest.SetBasicAuth(configuration.BasicUsername, string(request.Credential))
		return pro_interfaces.NotificationDispatchResult{}
	}
	key := canonicalServiceNowOrigin(configuration.InstanceOrigin) + "\x00" + configuration.ClientID + "\x00" + strconv.Itoa(request.DestinationConfigurationRevision) + "\x00" + configuration.Scope
	a.mu.Lock()
	token, cached := a.tokens[key]
	a.mu.Unlock()
	// A token accepted with the minimum 60-second lifetime remains valid for
	// the immediate POST following a successful preflight reconciliation.
	if !cached || time.Until(token.expires) <= 5*time.Second {
		var outcome pro_interfaces.NotificationDispatchResult
		token, outcome = a.token(ctx, configuration, request.Credential)
		if outcome.Outcome != "" {
			return outcome
		}
		a.mu.Lock()
		a.tokens[key] = token
		a.mu.Unlock()
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token.value)
	return pro_interfaces.NotificationDispatchResult{}
}

func (a *serviceNowAdapter) token(ctx context.Context, configuration *pro_interfaces.NotificationServiceNowConfiguration, secret []byte) (serviceNowToken, pro_interfaces.NotificationDispatchResult) {
	requestContext, cancel := context.WithTimeout(ctx, serviceNowRequestTimeout)
	defer cancel()
	values := url.Values{"grant_type": {"client_credentials"}, "client_id": {configuration.ClientID}, "client_secret": {string(secret)}}
	if configuration.Scope != "" {
		values.Set("scope", configuration.Scope)
	}
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, canonicalServiceNowOrigin(configuration.InstanceOrigin)+"/oauth_token.do", strings.NewReader(values.Encode()))
	if err != nil {
		return serviceNowToken{}, serviceNowPermanent()
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := a.client.Do(request)
	if err != nil || response == nil {
		return serviceNowToken{}, pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(response.Body, serviceNowResponseLimit))
		return serviceNowToken{}, serviceNowStatus(response.StatusCode, response.Header.Get("Retry-After"), time.Now())
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if !readServiceNowJSON(response.Body, &payload) || payload.AccessToken == "" || payload.ExpiresIn < 60 || payload.ExpiresIn > 86400 {
		return serviceNowToken{}, serviceNowPermanent()
	}
	return serviceNowToken{value: payload.AccessToken, expires: time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second)}, pro_interfaces.NotificationDispatchResult{}
}

func serviceNowPayload(request pro_interfaces.NotificationDispatchRequest) (map[string]string, bool) {
	if !serviceNowIncidentKey.MatchString(request.IncidentKey) {
		return nil, false
	}
	values := map[string]string{"correlation_id": request.IncidentKey}
	for _, mapping := range request.ServiceNow.FieldMappings {
		value := serviceNowSourceValue(request.Event, mapping.SourceField)
		if mapping.SourceField == pro_interfaces.NotificationServiceNowSourceSeverity {
			value = serviceNowSeverityPriority(request.Event.Severity)
		}
		if value == "" && mapping.SourceField == pro_interfaces.NotificationServiceNowSourceStatus {
			continue
		}
		if value == "" || len(value) > 1024 {
			return nil, false
		}
		values[string(mapping.IncidentField)] = value
	}
	return values, true
}

func serviceNowSeverityPriority(severity pro_interfaces.NotificationSeverity) string {
	switch severity {
	case pro_interfaces.NotificationSeverityCritical, pro_interfaces.NotificationSeverityError:
		return "1"
	case pro_interfaces.NotificationSeverityWarning:
		return "2"
	case pro_interfaces.NotificationSeverityInfo:
		return "3"
	default:
		return ""
	}
}

func serviceNowSourceValue(event pro_interfaces.NotificationEvent, source pro_interfaces.NotificationServiceNowSourceField) string {
	switch source {
	case pro_interfaces.NotificationServiceNowSourceSummary:
		if strings.HasPrefix(event.Source.ID, "system:notification_test-") {
			return "Semaphore controlled notification test"
		}
		return "Semaphore " + string(event.Severity) + " " + string(event.Source.Kind)
	case pro_interfaces.NotificationServiceNowSourceSeverity:
		return string(event.Severity)
	case pro_interfaces.NotificationServiceNowSourceLifecycleAction:
		return string(event.LifecycleAction)
	case pro_interfaces.NotificationServiceNowSourceStatus:
		return event.Details.Status
	default:
		return ""
	}
}

func serviceNowResponseID(response *http.Response) (string, bool) {
	defer response.Body.Close()
	var payload struct {
		Result struct {
			SysID string `json:"sys_id"`
		} `json:"result"`
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, serviceNowResponseLimit+1))
	if err != nil || len(contents) > serviceNowResponseLimit {
		return "", false
	}
	bodyIdentity := ""
	if len(contents) != 0 {
		decoder := json.NewDecoder(bytes.NewReader(contents))
		if decoder.Decode(&payload) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			return "", false
		}
		bodyIdentity = payload.Result.SysID
	}
	if bodyIdentity != "" && !serviceNowSysID.MatchString(bodyIdentity) {
		return "", false
	}
	location := response.Header.Get("Location")
	if location == "" {
		return bodyIdentity, bodyIdentity != ""
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || response.Request == nil || response.Request.URL == nil || (parsed.IsAbs() && (parsed.Scheme != response.Request.URL.Scheme || parsed.Host != response.Request.URL.Host)) || (!parsed.IsAbs() && parsed.Host != "") || !serviceNowSysID.MatchString(pathLast(parsed.Path)) {
		return "", false
	}
	locationIdentity := pathLast(parsed.Path)
	if parsed.Path != "/api/now/v1/table/incident/"+locationIdentity {
		return "", false
	}
	if bodyIdentity != "" && bodyIdentity != locationIdentity {
		return "", false
	}
	return locationIdentity, true
}

func pathLast(path string) string { return path[strings.LastIndex(path, "/")+1:] }

func serviceNowRecordURL(origin, recordID string) string {
	return origin + "/incident.do?sys_id=" + url.QueryEscape(recordID)
}

// readServiceNowJSON retains at most the fixed protocol allowance, detects an
// additional byte, and accepts exactly one complete JSON document.
func readServiceNowJSON(body io.Reader, target any) bool {
	contents, err := io.ReadAll(io.LimitReader(body, serviceNowResponseLimit+1))
	if err != nil || len(contents) > serviceNowResponseLimit {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	if decoder.Decode(target) != nil {
		return false
	}
	return decoder.Decode(&struct{}{}) == io.EOF
}

func serviceNowRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if seconds > int64(notificationDispatchMaxRetryAfter/time.Second) {
			return notificationDispatchMaxRetryAfter
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	delay := when.Sub(now)
	if delay > notificationDispatchMaxRetryAfter {
		return notificationDispatchMaxRetryAfter
	}
	return delay
}

func serviceNowPermanent() pro_interfaces.NotificationDispatchResult {
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchPermanent}
}

func serviceNowAmbiguous() pro_interfaces.NotificationDispatchResult {
	return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchAmbiguous}
}
func serviceNowStatus(status int, retryAfter string, now time.Time) pro_interfaces.NotificationDispatchResult {
	if status == http.StatusTooManyRequests {
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchRateLimited, RetryAfter: serviceNowRetryAfter(retryAfter, now)}
	}
	if status == http.StatusRequestTimeout || status == http.StatusTooEarly || status >= 500 {
		return pro_interfaces.NotificationDispatchResult{Outcome: pro_interfaces.NotificationDispatchTransient}
	}
	return serviceNowPermanent()
}

var _ pro_interfaces.NotificationLifecycleAdapter = (*serviceNowAdapter)(nil)
