package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditWebhookEndpointRequiresHTTPSWithoutEmbeddedCredentials(t *testing.T) {
	for _, endpoint := range []string{
		"", "http://audit.example.test/events", "https://", "https://user:secret@audit.example.test/events",
		"https://audit.example.test/events#fragment", "javascript:alert(1)",
	} {
		t.Run(endpoint, func(t *testing.T) {
			assert.Error(t, validateAuditWebhookEndpoint(endpoint))
		})
	}
	require.NoError(t, validateAuditWebhookEndpoint("https://audit.example.test/v1/events?tenant=acme"))
}

func TestAuditWebhookClientSendsBoundedAuthenticatedRequest(t *testing.T) {
	var authorization string
	var contentType string
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	client := testAuditWebhookClient(receiver, time.Second)

	result := client.Deliver(context.Background(), receiver.URL, "write-only-secret", []byte(`{"event_id":"event"}`))

	assert.True(t, result.Succeeded)
	require.NotNil(t, result.StatusCode)
	assert.Equal(t, http.StatusNoContent, *result.StatusCode)
	assert.Equal(t, "Bearer write-only-secret", authorization)
	assert.Equal(t, "application/json", contentType)
}

func TestAuditWebhookClientClassifiesRetryAndTerminalResponses(t *testing.T) {
	tests := []struct {
		status    int
		retryable bool
		reason    string
	}{
		{http.StatusRequestTimeout, true, auditWebhookReasonServerResponse},
		{http.StatusTooManyRequests, true, auditWebhookReasonServerResponse},
		{http.StatusServiceUnavailable, true, auditWebhookReasonServerResponse},
		{http.StatusTemporaryRedirect, false, auditWebhookReasonRedirect},
		{http.StatusBadRequest, false, auditWebhookReasonClientResponse},
		{http.StatusUnauthorized, false, auditWebhookReasonClientResponse},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("status_%d", test.status), func(t *testing.T) {
			receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer receiver.Close()
			client := testAuditWebhookClient(receiver, time.Second)

			result := client.Deliver(context.Background(), receiver.URL, "", []byte(`{}`))

			assert.False(t, result.Succeeded)
			assert.Equal(t, test.retryable, result.Retryable)
			assert.Equal(t, test.reason, result.Reason)
		})
	}
}

func TestAuditWebhookClientBoundsTimeoutAndResponseSize(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer receiver.Close()
		client := testAuditWebhookClient(receiver, 10*time.Millisecond)

		result := client.Deliver(context.Background(), receiver.URL, "", []byte(`{}`))

		assert.True(t, result.Retryable)
		assert.Equal(t, auditWebhookReasonTimeout, result.Reason)
	})

	t.Run("oversized response", func(t *testing.T) {
		receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strings.Repeat("x", auditWebhookResponseLimit+1)))
		}))
		defer receiver.Close()
		client := testAuditWebhookClient(receiver, time.Second)

		result := client.Deliver(context.Background(), receiver.URL, "", []byte(`{}`))

		assert.True(t, result.Retryable)
		assert.Equal(t, auditWebhookReasonResponseTooLarge, result.Reason)
	})
}

func TestAuditWebhookBackoffUsesBoundedEqualJitter(t *testing.T) {
	assert.Equal(t, 500*time.Millisecond, auditWebhookBackoff(1, 0))
	assert.Equal(t, time.Second, auditWebhookBackoff(1, 1))
	assert.Equal(t, time.Hour, auditWebhookBackoff(100, 1))
	assert.Equal(t, 30*time.Minute, auditWebhookBackoff(100, -1))
}

func testAuditWebhookClient(server *httptest.Server, timeout time.Duration) *httpsAuditWebhookClient {
	return &httpsAuditWebhookClient{httpClient: &http.Client{
		Transport: server.Client().Transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}
