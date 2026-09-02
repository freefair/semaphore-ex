package server

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type serviceNowRoundTripper func(*http.Request) (*http.Response, error)

func (fn serviceNowRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

const serviceNowTestIncidentKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const serviceNowTestRecordID = "0123456789abcdef0123456789abcdef"

func serviceNowTestConfiguration(mode pro_interfaces.NotificationServiceNowAuthMode) *pro_interfaces.NotificationServiceNowConfiguration {
	configuration := &pro_interfaces.NotificationServiceNowConfiguration{
		InstanceOrigin: "https://example.service-now.com", AuthMode: mode,
		FieldMappings: []pro_interfaces.NotificationServiceNowFieldMapping{{IncidentField: pro_interfaces.NotificationServiceNowIncidentShortDescription, SourceField: pro_interfaces.NotificationServiceNowSourceSummary}},
	}
	if mode == pro_interfaces.NotificationServiceNowAuthBasic {
		configuration.BasicUsername = "semaphore"
	} else {
		configuration.ClientID = "semaphore"
		configuration.Scope = "incident_read incident_write"
	}
	return configuration
}

func serviceNowTestRequest(configuration *pro_interfaces.NotificationServiceNowConfiguration) pro_interfaces.NotificationDispatchRequest {
	return pro_interfaces.NotificationDispatchRequest{Provider: serviceNowProviderName, IncidentKey: serviceNowTestIncidentKey, Credential: []byte("secret"), ServiceNow: configuration,
		Event: pro_interfaces.NotificationEvent{Severity: pro_interfaces.NotificationSeverityError, Source: pro_interfaces.NotificationSource{Kind: pro_interfaces.NotificationSourceTask}, LifecycleAction: pro_interfaces.NotificationLifecycleTrigger}}
}

func serviceNowResponse(status int, body string, headers map[string]string) *http.Response {
	header := make(http.Header)
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestServiceNowOriginValidation(t *testing.T) {
	for _, value := range []string{"https://tenant.service-now.com", "https://tenant.servicenow.com"} {
		assert.True(t, validServiceNowOrigin(value), value)
	}
	for _, value := range []string{"http://tenant.service-now.com", "https://user@tenant.service-now.com", "https://127.0.0.1", "https://tenant.service-now.com:444", "https://tenant.service-now.com/api", "https://tenant.service-now.com/?x=1", "https://tenant.service-now.com/#x", "https://evilservice-now.com", "https://example.invalid", "https://bad_label.service-now.com", "https://-bad.service-now.com", "https://bad-.service-now.com", "https://bad..service-now.com"} {
		assert.False(t, validServiceNowOrigin(value), value)
	}
	for _, value := range []string{"100.64.0.1", "192.0.2.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1", "2001:db8::1", "2001:2::1"} {
		assert.False(t, serviceNowPublicIP(net.ParseIP(value)), value)
	}
	assert.True(t, serviceNowPublicIP(net.ParseIP("8.8.8.8")))
}

func TestServiceNowTransportRejectsProxyRedirectAndNonPublicResolution(t *testing.T) {
	adapter := NewServiceNowAdapter().(*serviceNowAdapter)
	transport := adapter.client.Transport.(*http.Transport)
	assert.Nil(t, transport.Proxy)
	require.Error(t, adapter.client.CheckRedirect(&http.Request{}, nil))
	dialled := false
	dial := newServiceNowDialContext(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}, func(context.Context, string, string) (net.Conn, error) { dialled = true; return nil, nil })
	_, err := dial(context.Background(), "tcp", "tenant.service-now.com:443")
	assert.Error(t, err)
	assert.False(t, dialled)
	wantAddress := ""
	dial = newServiceNowDialContext(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}, func(_ context.Context, _ string, address string) (net.Conn, error) {
		wantAddress = address
		left, right := net.Pipe()
		right.Close()
		return left, nil
	})
	connection, err := dial(context.Background(), "tcp", "tenant.servicenow.com:443")
	require.NoError(t, err)
	connection.Close()
	assert.Equal(t, "8.8.8.8:443", wantAddress)
}

func TestServiceNowOAuthAndBasicNeverDowngrade(t *testing.T) {
	requests := make([]*http.Request, 0, 2)
	client := &http.Client{Transport: serviceNowRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		if request.URL.Path == "/oauth_token.do" {
			body, _ := io.ReadAll(request.Body)
			assert.Equal(t, "client_id=semaphore&client_secret=secret&grant_type=client_credentials&scope=incident_read+incident_write", string(body))
			return serviceNowResponse(http.StatusOK, `{"access_token":"token","expires_in":3600}`, nil), nil
		}
		return serviceNowResponse(http.StatusCreated, `{"result":{"sys_id":"`+serviceNowTestRecordID+`"}}`, nil), nil
	})}
	adapter := newServiceNowAdapter(client)
	result := adapter.Dispatch(context.Background(), serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials)))
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, result.Outcome)
	require.Len(t, requests, 2)
	assert.Equal(t, "Bearer token", requests[1].Header.Get("Authorization"))
	basicRequests := 0
	basic := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(request *http.Request) (*http.Response, error) {
		basicRequests++
		username, password, ok := request.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "semaphore", username)
		assert.Equal(t, "secret", password)
		return serviceNowResponse(http.StatusCreated, `{"result":{"sys_id":"`+serviceNowTestRecordID+`"}}`, nil), nil
	})})
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, basic.Dispatch(context.Background(), serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic))).Outcome)
	assert.Equal(t, 1, basicRequests)
	failedOAuth := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(request *http.Request) (*http.Response, error) {
		assert.Equal(t, "/oauth_token.do", request.URL.Path)
		return serviceNowResponse(http.StatusUnauthorized, "secret response", nil), nil
	})})
	assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, failedOAuth.Dispatch(context.Background(), serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials))).Outcome)
}

func TestServiceNowCreateIdentityAndStatusClassification(t *testing.T) {
	for _, test := range []struct {
		name, body, location string
		status               int
		want                 pro_interfaces.NotificationDispatchOutcome
	}{
		{"body", `{"result":{"sys_id":"` + serviceNowTestRecordID + `"}}`, "", http.StatusCreated, pro_interfaces.NotificationDispatchSucceeded},
		{"equal location", `{"result":{"sys_id":"` + serviceNowTestRecordID + `"}}`, "https://example.service-now.com/api/now/v1/table/incident/" + serviceNowTestRecordID, http.StatusCreated, pro_interfaces.NotificationDispatchSucceeded},
		{"mismatch", `{"result":{"sys_id":"` + serviceNowTestRecordID + `"}}`, "https://example.service-now.com/api/now/v1/table/incident/abcdefabcdefabcdefabcdefabcdefab", http.StatusCreated, pro_interfaces.NotificationDispatchAmbiguous},
		{"foreign", `{"result":{"sys_id":"` + serviceNowTestRecordID + `"}}`, "https://evil.service-now.com/api/now/v1/table/incident/" + serviceNowTestRecordID, http.StatusCreated, pro_interfaces.NotificationDispatchAmbiguous},
		{"missing", `{"result":{}}`, "", http.StatusCreated, pro_interfaces.NotificationDispatchAmbiguous},
		{"server", `{}`, "", http.StatusServiceUnavailable, pro_interfaces.NotificationDispatchTransient},
		{"conflict", `{}`, "", http.StatusConflict, pro_interfaces.NotificationDispatchAmbiguous},
		{"rate", `{}`, "", http.StatusTooManyRequests, pro_interfaces.NotificationDispatchRateLimited},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(*http.Request) (*http.Response, error) {
				headers := map[string]string{}
				if test.location != "" {
					headers["Location"] = test.location
				}
				return serviceNowResponse(test.status, test.body, headers), nil
			})})
			result := adapter.Dispatch(context.Background(), serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic)))
			assert.Equal(t, test.want, result.Outcome)
		})
	}
}

func TestServiceNowPayloadAndExactReconciliation(t *testing.T) {
	configuration := serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic)
	request := serviceNowTestRequest(configuration)
	payload, ok := serviceNowPayload(request)
	require.True(t, ok)
	assert.Equal(t, serviceNowTestIncidentKey, payload["correlation_id"])
	assert.Contains(t, payload["short_description"], "Semaphore")
	request.ServiceNow.FieldMappings[0].IncidentField = "work_notes"
	assert.False(t, validServiceNowConfiguration(request.ServiceNow), "configuration rejects append-only targets")
	for _, result := range []string{`{"result":[]}`, `{"result":[{"sys_id":"` + serviceNowTestRecordID + `","correlation_id":"` + serviceNowTestIncidentKey + `"}]}`, `{"result":[{"sys_id":"` + serviceNowTestRecordID + `","correlation_id":"` + serviceNowTestIncidentKey + `"},{"sys_id":"abcdefabcdefabcdefabcdefabcdefab","correlation_id":"` + serviceNowTestIncidentKey + `"}]}`} {
		adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(httpRequest *http.Request) (*http.Response, error) {
			assert.Equal(t, "2", httpRequest.URL.Query().Get("sysparm_limit"))
			assert.Equal(t, "sys_id,correlation_id", httpRequest.URL.Query().Get("sysparm_fields"))
			assert.Equal(t, "correlation_id="+serviceNowTestIncidentKey, httpRequest.URL.Query().Get("sysparm_query"))
			return serviceNowResponse(http.StatusOK, result, nil), nil
		})})
		outcome := adapter.ReconcileLifecycle(context.Background(), serviceNowTestRequest(configuration)).Result
		if strings.Contains(result, "},{") {
			assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, outcome.Outcome)
		} else {
			assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, outcome.Outcome)
		}
	}
}

func TestServiceNowReconciliationRejectsMalformedIncidentKeyBeforeRequest(t *testing.T) {
	calls := 0
	adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		return serviceNowResponse(http.StatusOK, `{"result":[]}`, nil), nil
	})})
	for _, incidentKey := range []string{strings.Repeat("A", 64), strings.Repeat("a", 63) + "?"} {
		request := serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic))
		request.IncidentKey = incidentKey
		result := adapter.ReconcileLifecycle(context.Background(), request)
		assert.Equal(t, pro_interfaces.NotificationDispatchPermanent, result.Result.Outcome, incidentKey)
	}
	assert.Zero(t, calls, "invalid identity must not reach a provider query")
}

func TestServiceNowMappingValidationAndControlledPayload(t *testing.T) {
	configuration := serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic)
	configuration.FieldMappings = []pro_interfaces.NotificationServiceNowFieldMapping{{IncidentField: pro_interfaces.NotificationServiceNowIncidentImpact, SourceField: pro_interfaces.NotificationServiceNowSourceSeverity}}
	assert.False(t, validServiceNowConfiguration(configuration), "short description is mandatory")
	configuration.FieldMappings = []pro_interfaces.NotificationServiceNowFieldMapping{{IncidentField: pro_interfaces.NotificationServiceNowIncidentShortDescription, SourceField: pro_interfaces.NotificationServiceNowSourceStatus}}
	assert.False(t, validServiceNowConfiguration(configuration), "the mandatory summary cannot depend on optional status")
	configuration.FieldMappings = []pro_interfaces.NotificationServiceNowFieldMapping{
		{IncidentField: pro_interfaces.NotificationServiceNowIncidentShortDescription, SourceField: pro_interfaces.NotificationServiceNowSourceSummary},
		{IncidentField: pro_interfaces.NotificationServiceNowIncidentImpact, SourceField: pro_interfaces.NotificationServiceNowSourceSummary},
	}
	assert.False(t, validServiceNowConfiguration(configuration), "priority fields accept only severity")
	configuration.FieldMappings[1].SourceField = pro_interfaces.NotificationServiceNowSourceSeverity
	configuration.FieldMappings = append(configuration.FieldMappings, pro_interfaces.NotificationServiceNowFieldMapping{IncidentField: pro_interfaces.NotificationServiceNowIncidentDescription, SourceField: pro_interfaces.NotificationServiceNowSourceStatus})
	require.True(t, validServiceNowConfiguration(configuration))
	payload, ok := serviceNowPayload(serviceNowTestRequest(configuration))
	require.True(t, ok, "the controlled test has no status")
	assert.Equal(t, "1", payload["impact"])
	assert.NotContains(t, payload, "description")
}

func TestServiceNowResponseLimitsIdentityAndRetryAfter(t *testing.T) {
	request := serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic))
	adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(*http.Request) (*http.Response, error) {
		return serviceNowResponse(http.StatusTooManyRequests, "", map[string]string{"Retry-After": "90"}), nil
	})})
	result := adapter.Dispatch(context.Background(), request)
	assert.Equal(t, pro_interfaces.NotificationDispatchRateLimited, result.Outcome)
	assert.Equal(t, 90*time.Second, result.RetryAfter)
	assert.Equal(t, notificationDispatchMaxRetryAfter, serviceNowRetryAfter("999999", time.Now()))
	assert.Zero(t, serviceNowRetryAfter("bad", time.Now()))

	response := serviceNowResponse(http.StatusCreated, "", map[string]string{"Location": "/api/now/v1/table/incident/" + serviceNowTestRecordID})
	response.Request, _ = http.NewRequest(http.MethodPost, "https://example.service-now.com/api/now/v1/table/incident", nil)
	id, ok := serviceNowResponseID(response)
	assert.True(t, ok)
	assert.Equal(t, serviceNowTestRecordID, id)
	foreignRelative := serviceNowResponse(http.StatusCreated, "", map[string]string{"Location": "//evil.service-now.com/api/now/v1/table/incident/" + serviceNowTestRecordID})
	foreignRelative.Request, _ = http.NewRequest(http.MethodPost, "https://example.service-now.com/api/now/v1/table/incident", nil)
	_, ok = serviceNowResponseID(foreignRelative)
	assert.False(t, ok)
	assert.False(t, readServiceNowJSON(strings.NewReader(`{"result":{}} {}`), &struct{}{}), "trailing JSON is rejected")
	assert.False(t, readServiceNowJSON(strings.NewReader(strings.Repeat("x", serviceNowResponseLimit+1)), &struct{}{}), "limit+1 detects oversized content")
}

func TestServiceNowOAuthTokenCacheFencesConfigurationGenerationAndScope(t *testing.T) {
	tokens := 0
	adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth_token.do" {
			tokens++
			return serviceNowResponse(http.StatusOK, `{"access_token":"token","expires_in":3600}`, nil), nil
		}
		return serviceNowResponse(http.StatusCreated, `{"result":{"sys_id":"`+serviceNowTestRecordID+`"}}`, nil), nil
	})})
	configuration := serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials)
	first := serviceNowTestRequest(configuration)
	first.DestinationConfigurationRevision = 1
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), first).Outcome)
	second := first
	second.DestinationConfigurationRevision = 2
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), second).Outcome)
	third := second
	third.ServiceNow = serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials)
	third.ServiceNow.Scope = "incident_write"
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, adapter.Dispatch(context.Background(), third).Outcome)
	assert.Equal(t, 3, tokens)
}

func TestServiceNowPreflightAndCreateReuseMinimumLifetimeOAuthToken(t *testing.T) {
	tokenRequests, reconciliationRequests, createRequests := 0, 0, 0
	adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/oauth_token.do":
			tokenRequests++
			return serviceNowResponse(http.StatusOK, `{"access_token":"token","expires_in":60}`, nil), nil
		case "/api/now/v1/table/incident":
			if request.Method == http.MethodGet {
				reconciliationRequests++
				return serviceNowResponse(http.StatusOK, `{"result":[]}`, nil), nil
			}
			createRequests++
			return serviceNowResponse(http.StatusCreated, `{"result":{"sys_id":"`+serviceNowTestRecordID+`"}}`, nil), nil
		default:
			t.Fatalf("unexpected ServiceNow request: %s", request.URL.Path)
			return nil, nil
		}
	})})
	request := serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials))
	reconciliation := adapter.ReconcileLifecycle(context.Background(), request)
	require.Equal(t, pro_interfaces.NotificationDispatchSucceeded, reconciliation.Result.Outcome)
	assert.False(t, reconciliation.Found)
	result := adapter.Dispatch(context.Background(), request)
	assert.Equal(t, pro_interfaces.NotificationDispatchSucceeded, result.Outcome)
	assert.Equal(t, 1, tokenRequests)
	assert.Equal(t, 1, reconciliationRequests)
	assert.Equal(t, 1, createRequests)
}

func TestServiceNowOAuthFailureClassificationDoesNotDowngrade(t *testing.T) {
	for _, test := range []struct {
		name     string
		response *http.Response
		err      error
		want     pro_interfaces.NotificationDispatchOutcome
	}{
		{name: "network", err: errors.New("network"), want: pro_interfaces.NotificationDispatchTransient},
		{name: "rate", response: serviceNowResponse(http.StatusTooManyRequests, "", map[string]string{"Retry-After": "30"}), want: pro_interfaces.NotificationDispatchRateLimited},
		{name: "server", response: serviceNowResponse(http.StatusServiceUnavailable, "", nil), want: pro_interfaces.NotificationDispatchTransient},
		{name: "unauthorized", response: serviceNowResponse(http.StatusUnauthorized, "", nil), want: pro_interfaces.NotificationDispatchPermanent},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				assert.Equal(t, "/oauth_token.do", request.URL.Path)
				assert.Empty(t, request.Header.Get("Authorization"))
				_, _, basic := request.BasicAuth()
				assert.False(t, basic)
				return test.response, test.err
			})})
			result := adapter.Dispatch(context.Background(), serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthOAuthClientCredentials)))
			assert.Equal(t, test.want, result.Outcome)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestServiceNowControlledTestSummaryIsBoundedAndExplicit(t *testing.T) {
	event := serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic)).Event
	event.Source.ID = "system:notification_test-42-aaaaaaaaaaaaaaaaaaaaaaaa"
	summary := serviceNowSourceValue(event, pro_interfaces.NotificationServiceNowSourceSummary)
	assert.Equal(t, "Semaphore controlled notification test", summary)
	assert.LessOrEqual(t, len(summary), 1024)
}

func TestServiceNowNetworkFailureIsTransientWithoutResponseDisclosure(t *testing.T) {
	adapter := newServiceNowAdapter(&http.Client{Transport: serviceNowRoundTripper(func(*http.Request) (*http.Response, error) { return nil, errors.New("network secret") })})
	result := adapter.Dispatch(context.Background(), serviceNowTestRequest(serviceNowTestConfiguration(pro_interfaces.NotificationServiceNowAuthBasic)))
	assert.Equal(t, pro_interfaces.NotificationDispatchTransient, result.Outcome)
	assert.Empty(t, result.RecordID)
	assert.Empty(t, result.RequestID)
}
