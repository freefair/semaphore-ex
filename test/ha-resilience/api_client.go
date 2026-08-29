package haresilience

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

type apiClient struct {
	client *http.Client
}

func newAPIClient() (*apiClient, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &apiClient{client: &http.Client{Jar: jar, Timeout: 8 * time.Second}}, nil
}

func (c *apiClient) login(ctx context.Context, baseURL string, password string) error {
	var response map[string]any
	return c.json(ctx, http.MethodPost, baseURL+"/api/auth/login", map[string]any{
		"auth": "admin", "password": password, "method": "password",
	}, &response, http.StatusNoContent, http.StatusOK)
}

func (c *apiClient) json(ctx context.Context, method string, url string, body any, result any, expected ...int) error {
	status, responseBody, err := c.request(ctx, method, url, body)
	if err != nil {
		return err
	}
	accepted := false
	for _, code := range expected {
		if status == code {
			accepted = true
			break
		}
	}
	if !accepted {
		return fmt.Errorf("%s %s returned %d: %s", method, url, status, responseBody)
	}
	if result != nil && len(bytes.TrimSpace(responseBody)) > 0 {
		if err := json.Unmarshal(responseBody, result); err != nil {
			return fmt.Errorf("decode %s %s response: %w: %s", method, url, err, responseBody)
		}
	}
	return nil
}

func (c *apiClient) request(ctx context.Context, method string, url string, body any) (int, []byte, error) {
	return c.requestWithHeaders(ctx, method, url, body, nil)
}

func (c *apiClient) requestWithHeaders(ctx context.Context, method string, url string, body any, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, responseBody, nil
}

func (c *apiClient) waitStatus(ctx context.Context, url string, expected int) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		status, body, err := c.request(ctx, http.MethodGet, url, nil)
		if err == nil && status == expected {
			return nil
		}
		if err != nil {
			last = err.Error()
		} else {
			last = fmt.Sprintf("status %d: %s", status, strings.TrimSpace(string(body)))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s status %d: %s: %w", url, expected, last, ctx.Err())
		case <-ticker.C:
		}
	}
}
