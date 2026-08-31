package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type pagerDutyRecordedRequest struct {
	URL    string
	Method string
	Body   string
}

type pagerDutyRoundTripper struct {
	mu       sync.Mutex
	requests []pagerDutyRecordedRequest
	response func(*http.Request) (*http.Response, error)
}

func (t *pagerDutyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(request.Body)
	t.mu.Lock()
	t.requests = append(t.requests, pagerDutyRecordedRequest{URL: request.URL.String(), Method: request.Method, Body: string(body)})
	t.mu.Unlock()
	return t.response(request)
}

func (t *pagerDutyRoundTripper) last(tester *testing.T) pagerDutyRecordedRequest {
	tester.Helper()
	t.mu.Lock()
	defer t.mu.Unlock()
	require.NotEmpty(tester, t.requests)
	return t.requests[len(t.requests)-1]
}

func pagerDutyResponse(status int, headers http.Header, body string) (*http.Response, error) {
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
}

func pagerDutyTestAdapter(roundTripper *pagerDutyRoundTripper) *pagerDutyAdapter {
	return newPagerDutyAdapter(&http.Client{Transport: roundTripper}, func() time.Time {
		return time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	})
}

func pagerDutyDispatchRequest(region pro_interfaces.NotificationProviderRegion, action pro_interfaces.NotificationLifecycleAction) pro_interfaces.NotificationDispatchRequest {
	incident := "incident-0123456789abcdef"
	return pro_interfaces.NotificationDispatchRequest{
		Provider: pagerDutyProviderName, Region: region, IncidentKey: incident, Credential: []byte("0123456789ABCDEF0123456789ABCDEF"),
		Event: pro_interfaces.NotificationEvent{
			SchemaVersion: pro_interfaces.NotificationSchemaVersion, EventID: "event-0123456789abcdef", SourceEventKey: "source-0123456789abcdef",
			SourceRevision: 1, OccurredAt: time.Date(2026, time.August, 31, 11, 59, 0, 0, time.UTC), Scope: pro_interfaces.NotificationScopeGlobal,
			Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask, ID: "task:42"}, LifecycleID: "template:7",
			Severity: pro_interfaces.NotificationSeverityError, LifecycleAction: action, IncidentKey: incident,
			Details: pro_interfaces.NotificationDetails{Status: "failed"},
		},
	}
}

func TestPagerDutyUsesOnlyFixedRegionalEndpointsAndSafePayload(t *testing.T) {
	for _, test := range []struct {
		region pro_interfaces.NotificationProviderRegion
		url    string
	}{
		{pro_interfaces.NotificationProviderRegionUS, pagerDutyUSURL},
		{pro_interfaces.NotificationProviderRegionEU, pagerDutyEUURL},
	} {
		t.Run(string(test.region), func(t *testing.T) {
			roundTripper := &pagerDutyRoundTripper{response: func(*http.Request) (*http.Response, error) {
				return pagerDutyResponse(http.StatusAccepted, nil, "not-json")
			}}
			result := pagerDutyTestAdapter(roundTripper).Dispatch(context.Background(), pagerDutyDispatchRequest(test.region, pro_interfaces.NotificationLifecycleTrigger))
			require.Equal(t, pro_interfaces.NotificationDispatchSucceeded, result.Outcome)
			recorded := roundTripper.last(t)
			assert.Equal(t, http.MethodPost, recorded.Method)
			assert.Equal(t, test.url, recorded.URL)
			assert.Contains(t, recorded.Body, `"event_action":"trigger"`)
			assert.Contains(t, recorded.Body, `"dedup_key":"incident-0123456789abcdef"`)
			assert.Contains(t, recorded.Body, `"source":"task:42"`)
			assert.NotContains(t, recorded.Body, `"message"`)
			assert.LessOrEqual(t, len(recorded.Body), pagerDutyMaxPayloadBytes)
		})
	}
}

func TestPagerDutyMapsLifecycleToStableDedupKey(t *testing.T) {
	roundTripper := &pagerDutyRoundTripper{response: func(*http.Request) (*http.Response, error) {
		return pagerDutyResponse(http.StatusAccepted, nil, "{}")
	}}
	adapter := pagerDutyTestAdapter(roundTripper)
	for _, action := range []pro_interfaces.NotificationLifecycleAction{
		pro_interfaces.NotificationLifecycleTrigger, pro_interfaces.NotificationLifecycleUpdate, pro_interfaces.NotificationLifecycleResolve,
	} {
		result := adapter.Dispatch(context.Background(), pagerDutyDispatchRequest(pro_interfaces.NotificationProviderRegionUS, action))
		require.Equal(t, pro_interfaces.NotificationDispatchSucceeded, result.Outcome)
	}
	roundTripper.mu.Lock()
	defer roundTripper.mu.Unlock()
	require.Len(t, roundTripper.requests, 3)
	assert.Contains(t, roundTripper.requests[0].Body, `"event_action":"trigger"`)
	assert.Contains(t, roundTripper.requests[1].Body, `"event_action":"trigger"`)
	assert.Contains(t, roundTripper.requests[2].Body, `"event_action":"resolve"`)
	for _, request := range roundTripper.requests {
		assert.Contains(t, request.Body, `"dedup_key":"incident-0123456789abcdef"`)
	}
}

func TestPagerDutyClassifiesResponsesAndBoundsRetryAfter(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		retryAfter string
		outcome    pro_interfaces.NotificationDispatchOutcome
		delay      time.Duration
	}{
		{"accepted malformed", http.StatusAccepted, "", pro_interfaces.NotificationDispatchSucceeded, 0},
		{"accepted oversized", http.StatusAccepted, "", pro_interfaces.NotificationDispatchSucceeded, 0},
		{"bad request", http.StatusBadRequest, "", pro_interfaces.NotificationDispatchPermanent, 0},
		{"unauthorized", http.StatusUnauthorized, "", pro_interfaces.NotificationDispatchPermanent, 0},
		{"forbidden", http.StatusForbidden, "", pro_interfaces.NotificationDispatchPermanent, 0},
		{"not found", http.StatusNotFound, "", pro_interfaces.NotificationDispatchPermanent, 0},
		{"timeout", http.StatusRequestTimeout, "", pro_interfaces.NotificationDispatchTransient, 0},
		{"too early", http.StatusTooEarly, "", pro_interfaces.NotificationDispatchTransient, 0},
		{"server error", http.StatusBadGateway, "", pro_interfaces.NotificationDispatchTransient, 0},
		{"rate limit seconds", http.StatusTooManyRequests, "7", pro_interfaces.NotificationDispatchRateLimited, 7 * time.Second},
		{"rate limit date", http.StatusTooManyRequests, "Sun, 31 Aug 2026 12:00:30 GMT", pro_interfaces.NotificationDispatchRateLimited, 30 * time.Second},
		{"rate limit bound", http.StatusTooManyRequests, "999999999999", pro_interfaces.NotificationDispatchRateLimited, pagerDutyMaxRetryAfter},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := "malformed"
			if test.name == "accepted oversized" {
				body = strings.Repeat("x", pagerDutyMaxResponseBytes+1)
			}
			roundTripper := &pagerDutyRoundTripper{response: func(*http.Request) (*http.Response, error) {
				return pagerDutyResponse(test.status, http.Header{"Retry-After": []string{test.retryAfter}}, body)
			}}
			result := pagerDutyTestAdapter(roundTripper).Dispatch(context.Background(), pagerDutyDispatchRequest(pro_interfaces.NotificationProviderRegionUS, pro_interfaces.NotificationLifecycleTrigger))
			assert.Equal(t, test.outcome, result.Outcome)
			assert.Equal(t, test.delay, result.RetryAfter)
		})
	}
}

func TestPagerDutyRejectsInvalidInputWithoutOutboundRequest(t *testing.T) {
	roundTripper := &pagerDutyRoundTripper{response: func(*http.Request) (*http.Response, error) {
		return pagerDutyResponse(http.StatusAccepted, nil, "{}")
	}}
	adapter := pagerDutyTestAdapter(roundTripper)
	for _, modify := range []func(*pro_interfaces.NotificationDispatchRequest){
		func(request *pro_interfaces.NotificationDispatchRequest) { request.Credential = []byte("short") },
		func(request *pro_interfaces.NotificationDispatchRequest) { request.Region = "unexpected" },
		func(request *pro_interfaces.NotificationDispatchRequest) { request.Event.IncidentKey = "other" },
		func(request *pro_interfaces.NotificationDispatchRequest) {
			request.Event.Details.Message = "raw details are forbidden"
		},
	} {
		request := pagerDutyDispatchRequest(pro_interfaces.NotificationProviderRegionUS, pro_interfaces.NotificationLifecycleTrigger)
		modify(&request)
		assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, adapter.Dispatch(context.Background(), request).Outcome)
	}
	roundTripper.mu.Lock()
	defer roundTripper.mu.Unlock()
	assert.Empty(t, roundTripper.requests)
}

func TestPagerDutyRejectsRedirectsAndRedactsProviderResponses(t *testing.T) {
	routingKey := "0123456789ABCDEF0123456789ABCDEF"
	roundTripper := &pagerDutyRoundTripper{response: func(*http.Request) (*http.Response, error) {
		return pagerDutyResponse(http.StatusFound, http.Header{"Location": []string{"https://attacker.example/"}}, "provider response "+routingKey)
	}}
	result := pagerDutyTestAdapter(roundTripper).Dispatch(context.Background(), pagerDutyDispatchRequest(pro_interfaces.NotificationProviderRegionUS, pro_interfaces.NotificationLifecycleTrigger))
	assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, result.Outcome)
	assert.NotContains(t, fmt.Sprint(result), routingKey)
	roundTripper.mu.Lock()
	defer roundTripper.mu.Unlock()
	require.Len(t, roundTripper.requests, 1)
	assert.Equal(t, pagerDutyUSURL, roundTripper.requests[0].URL)
}

func TestPagerDutyTreatsNetworkAndContextTimeoutsAsTransient(t *testing.T) {
	for _, transportError := range []error{errors.New("network unavailable"), context.DeadlineExceeded} {
		roundTripper := &pagerDutyRoundTripper{response: func(*http.Request) (*http.Response, error) {
			return nil, transportError
		}}
		result := pagerDutyTestAdapter(roundTripper).Dispatch(context.Background(), pagerDutyDispatchRequest(pro_interfaces.NotificationProviderRegionUS, pro_interfaces.NotificationLifecycleTrigger))
		assert.Equal(t, pro_interfaces.NotificationDispatchTransient, result.Outcome)
	}
}

func TestPagerDutyBoundsTypedFieldsAndLabelsTestEvents(t *testing.T) {
	request := pagerDutyDispatchRequest(pro_interfaces.NotificationProviderRegionUS, pro_interfaces.NotificationLifecycleResolve)
	request.Event.Source = pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceSystem, ID: "system:notification_test-7-abcdef"}
	request.Event.LifecycleID = strings.Repeat("lifecycle-", 100)
	request.Event.Details.Status = strings.Repeat("status-", 100)
	body, _, valid := pagerDutyRequestBody(request)
	require.True(t, valid)
	assert.LessOrEqual(t, len(body), pagerDutyMaxPayloadBytes)
	assert.Contains(t, string(body), `"summary":"Semaphore notification test"`)
	assert.NotContains(t, string(body), `"message"`)
}
