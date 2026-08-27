package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	auditWebhookRequestTimeout = 5 * time.Second
	auditWebhookResponseLimit  = 64 * 1024
	auditWebhookEndpointLimit  = 2048

	auditWebhookReasonTimeout           = "timeout"
	auditWebhookReasonNetwork           = "network_error"
	auditWebhookReasonResponseTooLarge  = "response_too_large"
	auditWebhookReasonClientResponse    = "client_response"
	auditWebhookReasonServerResponse    = "server_response"
	auditWebhookReasonRedirect          = "redirect_response"
	auditWebhookReasonAttemptsExhausted = "attempts_exhausted"
	auditWebhookReasonConfiguration     = "configuration_error"
)

type auditWebhookDeliveryResult struct {
	Succeeded  bool
	Retryable  bool
	StatusCode *int
	Reason     string
}

type auditWebhookClient interface {
	Deliver(context.Context, string, string, []byte) auditWebhookDeliveryResult
}

type httpsAuditWebhookClient struct {
	httpClient *http.Client
}

func newHTTPSAuditWebhookClient() *httpsAuditWebhookClient {
	return &httpsAuditWebhookClient{httpClient: &http.Client{
		Timeout: auditWebhookRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func validateAuditWebhookEndpoint(endpoint string) error {
	if endpoint == "" || len(endpoint) > auditWebhookEndpointLimit {
		return fmt.Errorf("audit webhook endpoint is required")
	}
	if strings.Contains(endpoint, "#") {
		return fmt.Errorf("audit webhook endpoint must be an HTTPS URL without credentials or fragments")
	}
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return fmt.Errorf("audit webhook endpoint must be an HTTPS URL without credentials or fragments")
	}
	return nil
}

func (c *httpsAuditWebhookClient) Deliver(ctx context.Context, endpoint, credential string, payload []byte) auditWebhookDeliveryResult {
	if err := validateAuditWebhookEndpoint(endpoint); err != nil {
		return auditWebhookDeliveryResult{Reason: auditWebhookReasonConfiguration}
	}
	requestContext, cancel := context.WithTimeout(ctx, auditWebhookRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return auditWebhookDeliveryResult{Reason: auditWebhookReasonConfiguration}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Semaphore-Audit-Webhook/1")
	if credential != "" {
		request.Header.Set("Authorization", "Bearer "+credential)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		if requestContext.Err() != nil || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return auditWebhookDeliveryResult{Retryable: true, Reason: auditWebhookReasonTimeout}
		}
		return auditWebhookDeliveryResult{Retryable: true, Reason: auditWebhookReasonNetwork}
	}
	defer response.Body.Close()
	statusCode := response.StatusCode
	body, readErr := io.ReadAll(io.LimitReader(response.Body, auditWebhookResponseLimit+1))
	if readErr != nil {
		return auditWebhookDeliveryResult{Retryable: true, StatusCode: &statusCode, Reason: auditWebhookReasonNetwork}
	}
	if len(body) > auditWebhookResponseLimit {
		return auditWebhookDeliveryResult{Retryable: true, StatusCode: &statusCode, Reason: auditWebhookReasonResponseTooLarge}
	}

	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return auditWebhookDeliveryResult{Succeeded: true, StatusCode: &statusCode}
	case statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooEarly || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError:
		return auditWebhookDeliveryResult{Retryable: true, StatusCode: &statusCode, Reason: auditWebhookReasonServerResponse}
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return auditWebhookDeliveryResult{StatusCode: &statusCode, Reason: auditWebhookReasonRedirect}
	default:
		return auditWebhookDeliveryResult{StatusCode: &statusCode, Reason: auditWebhookReasonClientResponse}
	}
}
