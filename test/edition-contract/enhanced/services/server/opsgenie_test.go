package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type opsgenieRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip opsgenieRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestOpsgenieAdapterCreatesAliasWithWriteOnlyKeyAndPendingRequest(t *testing.T) {
	key := "write-only-key"
	var requestBody string
	adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, opsgenieUSURL+"/v2/alerts", request.URL.String())
		assert.Equal(t, "GenieKey "+key, request.Header.Get("Authorization"))
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		requestBody = string(body)
		return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"requestId":"request_1"}`))}, nil
	})}, time.Now)
	result := adapter.Dispatch(context.Background(), opsgenieRequest(key))
	assert.Equal(t, pro_interfaces.NotificationDispatchPending, result.Outcome)
	assert.Equal(t, "request_1", result.RequestID)
	assert.Contains(t, requestBody, `"alias":"`+strings.Repeat("a", 64)+`"`)
	assert.NotContains(t, requestBody, key)
}

func TestOpsgenieAdapterUsesBothRegionsAndStableAliasForCreateAndUpdate(t *testing.T) {
	for _, region := range []pro_interfaces.NotificationProviderRegion{pro_interfaces.NotificationProviderRegionUS, pro_interfaces.NotificationProviderRegionEU} {
		t.Run(string(region), func(t *testing.T) {
			aliases := []string{}
			adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(request *http.Request) (*http.Response, error) {
				if region == pro_interfaces.NotificationProviderRegionUS {
					assert.True(t, strings.HasPrefix(request.URL.String(), opsgenieUSURL))
				} else {
					assert.True(t, strings.HasPrefix(request.URL.String(), opsgenieEUURL))
				}
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				var payload struct {
					Alias string `json:"alias"`
				}
				require.NoError(t, json.Unmarshal(body, &payload))
				aliases = append(aliases, payload.Alias)
				return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"requestId":"request_1"}`))}, nil
			})}, time.Now)
			create := opsgenieRequest("write-only-key")
			create.Region = region
			update := create
			update.Event.LifecycleAction = pro_interfaces.NotificationLifecycleUpdate
			assert.Equal(t, pro_interfaces.NotificationDispatchPending, adapter.Dispatch(context.Background(), create).Outcome)
			assert.Equal(t, pro_interfaces.NotificationDispatchPending, adapter.Dispatch(context.Background(), update).Outcome)
			require.Len(t, aliases, 2)
			assert.Equal(t, aliases[0], aliases[1])
		})
	}
}

func TestOpsgenieAdapterPollsTerminalSuccessAndClosesMissingAlias(t *testing.T) {
	key := "write-only-key"
	adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			assert.Contains(t, request.URL.String(), "/v2/alerts/requests/request_1")
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":{"isSuccess":true}}`))}, nil
		}
		assert.Contains(t, request.URL.String(), "/close?identifierType=alias")
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}, time.Now)
	request := opsgenieRequest(key)
	request.ProviderRequestID = "request_1"
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), request).Outcome)
	request.ProviderRequestID = ""
	request.Event.LifecycleAction = pro_interfaces.NotificationLifecycleResolve
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), request).Outcome)
}

func TestOpsgenieAdapterClosesAliasAsynchronously(t *testing.T) {
	calls := 0
	adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			assert.Equal(t, http.MethodPost, request.Method)
			assert.Contains(t, request.URL.String(), "/close?identifierType=alias")
			return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"requestId":"request_1"}`))}, nil
		}
		assert.Equal(t, http.MethodGet, request.Method)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":{"isSuccess":true,"status":"successful"}}`))}, nil
	})}, time.Now)
	request := opsgenieRequest("write-only-key")
	request.Event.LifecycleAction = pro_interfaces.NotificationLifecycleResolve
	first := adapter.Dispatch(context.Background(), request)
	assert.Equal(t, pro_interfaces.NotificationDispatchPending, first.Outcome)
	request.ProviderRequestID = first.RequestID
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), request).Outcome)
}

func TestOpsgenieRequestStatusUsesDataStatusForExactResolveIdempotency(t *testing.T) {
	key := "write-only-key"
	adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"data":{"isSuccess":false,"status":"Alert does not exist"}}`))}, nil
	})}, time.Now)
	resolve := opsgenieRequest(key)
	resolve.Event.LifecycleAction = pro_interfaces.NotificationLifecycleResolve
	resolve.ProviderRequestID = "request_1"
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), resolve).Outcome)
	create := opsgenieRequest(key)
	create.ProviderRequestID = "request_1"
	assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, adapter.Dispatch(context.Background(), create).Outcome)
}

func TestOpsgenieConfigurationRejectsProviderConfusionAndUnsafeResponders(t *testing.T) {
	pagerDuty := destinationInput(nil)
	pagerDuty.Opsgenie = &pro_interfaces.NotificationOpsgenieConfiguration{}
	assert.ErrorIs(t, validateDestinationInput(nil, pagerDuty), pro_interfaces.ErrNotificationInvalidInput)
	config := &pro_interfaces.NotificationOpsgenieConfiguration{Responders: []pro_interfaces.NotificationOpsgenieResponder{{Type: pro_interfaces.NotificationOpsgenieResponderUser, ID: "id", Username: "also-set"}}}
	assert.False(t, validOpsgenieConfiguration(config))
}

func TestOpsgeniePayloadBoundsTagsPriorityAndMultibyteTruncation(t *testing.T) {
	request := opsgenieRequest("write-only-key")
	request.Event.EventID = strings.Repeat("x", 100)
	request.Event.Source.ID = strings.Repeat("é", 80)
	request.Event.LifecycleID = strings.Repeat("界", 300)
	request.Opsgenie = &pro_interfaces.NotificationOpsgenieConfiguration{Priority: pro_interfaces.NotificationOpsgeniePriorityP4}
	payload, ok := opsgenieCreatePayload(request)
	require.True(t, ok)
	assert.LessOrEqual(t, len(payload.Message), 130)
	assert.LessOrEqual(t, len(payload.Alias), 512)
	assert.LessOrEqual(t, len(payload.Description), 15000)
	assert.LessOrEqual(t, len(payload.Entity), 512)
	assert.LessOrEqual(t, len(payload.Source), 100)
	assert.LessOrEqual(t, len(payload.Tags), 20)
	for _, tag := range payload.Tags {
		assert.LessOrEqual(t, len(tag), 50)
	}
	assert.Equal(t, "P4", payload.Priority)
	assert.True(t, utf8.ValidString(payload.Entity))
	assert.True(t, utf8.ValidString(payload.Source))
	assert.Equal(t, "P1", opsgeniePriority(pro_interfaces.NotificationSeverityCritical))
	assert.Equal(t, "P2", opsgeniePriority(pro_interfaces.NotificationSeverityError))
	assert.Equal(t, "P3", opsgeniePriority(pro_interfaces.NotificationSeverityWarning))
	assert.Equal(t, "P5", opsgeniePriority(pro_interfaces.NotificationSeverityInfo))
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), 32*1024)
	details, err := json.Marshal(payload.Details)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(details), 8000)
}

func TestOpsgenieAdapterClassifiesAsyncBodiesAndHTTPResponses(t *testing.T) {
	request := opsgenieRequest("write-only-key")
	for _, test := range []struct {
		name      string
		requestID string
		status    int
		body      string
		header    http.Header
		want      pro_interfaces.NotificationDispatchOutcome
	}{
		{"submit malformed", "", http.StatusAccepted, "{", nil, pro_interfaces.NotificationDispatchPermanent},
		{"submit oversized request ID", "", http.StatusAccepted, `{"requestId":"` + strings.Repeat("a", 129) + `"}`, nil, pro_interfaces.NotificationDispatchPermanent},
		{"poll accepted", "request_1", http.StatusAccepted, `{}`, nil, pro_interfaces.NotificationDispatchPending},
		{"poll missing", "request_1", http.StatusNotFound, `{}`, nil, pro_interfaces.NotificationDispatchPending},
		{"poll false", "request_1", http.StatusOK, `{"data":{"isSuccess":false,"status":"denied"}}`, nil, pro_interfaces.NotificationDispatchPermanent},
		{"rate x", "request_1", http.StatusTooManyRequests, `{}`, http.Header{"X-RateLimit-Period-In-Sec": []string{"4"}}, pro_interfaces.NotificationDispatchRateLimited},
		{"rate retry", "request_1", http.StatusTooManyRequests, `{}`, http.Header{"Retry-After": []string{"5"}}, pro_interfaces.NotificationDispatchRateLimited},
		{"timeout", "request_1", http.StatusRequestTimeout, `{}`, nil, pro_interfaces.NotificationDispatchTransient},
		{"too early", "request_1", http.StatusTooEarly, `{}`, nil, pro_interfaces.NotificationDispatchTransient},
		{"server", "request_1", http.StatusBadGateway, `{}`, nil, pro_interfaces.NotificationDispatchTransient},
		{"redirect", "request_1", http.StatusFound, `{}`, nil, pro_interfaces.NotificationDispatchPermanent},
		{"client", "request_1", http.StatusUnauthorized, `{}`, nil, pro_interfaces.NotificationDispatchPermanent},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: test.header, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}, time.Now)
			current := request
			current.ProviderRequestID = test.requestID
			assert.Equal(t, test.want, adapter.Dispatch(context.Background(), current).Outcome)
		})
	}
}

func TestOpsgenieAdapterRejectsInvalidKeyAndOversizedBodiesBeforePersistence(t *testing.T) {
	calls := 0
	adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", opsgenieMaxResponseBytes+1)))}, nil
	})}, time.Now)
	invalid := opsgenieRequest("line\nbreak")
	assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, adapter.Dispatch(context.Background(), invalid).Outcome)
	assert.Zero(t, calls)
	valid := opsgenieRequest("write-only-key")
	assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, adapter.Dispatch(context.Background(), valid).Outcome)
	assert.Equal(t, 1, calls)
	poll := valid
	poll.ProviderRequestID = "request_1"
	assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, adapter.Dispatch(context.Background(), poll).Outcome)
	assert.Equal(t, 2, calls)
}

func TestOpsgenieAdapterTreatsNetworkErrorsAsTransient(t *testing.T) {
	adapter := newOpsgenieAdapter(&http.Client{Transport: opsgenieRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}, time.Now)
	assert.Equal(t, pro_interfaces.NotificationDispatchTransient, adapter.Dispatch(context.Background(), opsgenieRequest("write-only-key")).Outcome)
}

func TestOpsgenieConfigurationBoundsResponders(t *testing.T) {
	responders := make([]pro_interfaces.NotificationOpsgenieResponder, 50)
	for index := range responders {
		responders[index] = pro_interfaces.NotificationOpsgenieResponder{Type: pro_interfaces.NotificationOpsgenieResponderTeam, ID: "team"}
	}
	assert.True(t, validOpsgenieConfiguration(&pro_interfaces.NotificationOpsgenieConfiguration{Responders: responders}))
	responders = append(responders, responders[0])
	assert.False(t, validOpsgenieConfiguration(&pro_interfaces.NotificationOpsgenieConfiguration{Responders: responders}))
	assert.False(t, validOpsgenieConfiguration(&pro_interfaces.NotificationOpsgenieConfiguration{Responders: []pro_interfaces.NotificationOpsgenieResponder{{Type: pro_interfaces.NotificationOpsgenieResponderSchedule, Username: "user"}}}))
	assert.False(t, validOpsgenieConfiguration(&pro_interfaces.NotificationOpsgenieConfiguration{Responders: []pro_interfaces.NotificationOpsgenieResponder{{Type: pro_interfaces.NotificationOpsgenieResponderTeam, Name: " trim "}}}))
}

func opsgenieRequest(key string) pro_interfaces.NotificationDispatchRequest {
	event := taskEvent(nil)
	event.SchemaVersion = pro_interfaces.NotificationSchemaVersion
	event.EventID = strings.Repeat("b", 32)
	event.SourceEventKey = strings.Repeat("c", 64)
	event.IncidentKey = strings.Repeat("a", 64)
	event.OccurredAt = time.Now().UTC()
	return pro_interfaces.NotificationDispatchRequest{Event: event, Provider: opsgenieProviderName, Region: pro_interfaces.NotificationProviderRegionUS, IncidentKey: event.IncidentKey, Credential: []byte(key)}
}
