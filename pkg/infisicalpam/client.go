// Package infisicalpam connects task SSH traffic to targets through Infisical
// PAM. The Infisical gateway injects the target credential, so neither
// Semaphore nor the runner holds it.
//
// The wire protocol follows the MIT-licensed client in the Infisical CLI
// (github.com/Infisical/cli, packages/pam). It is not a published API and is
// isolated here so a protocol change touches one package.
package infisicalpam

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultTimeout      = 10 * time.Second
	MaximumTimeout      = 60 * time.Second
	maximumResponseSize = int64(1 << 20)
	maximumReasonLength = 500
	userAgent           = "semaphore-ex-infisical-pam"

	// MaximumSessionDuration bounds one PAM session; Infisical templates may
	// impose a shorter limit.
	MaximumSessionDuration = 24 * time.Hour
)

// ErrorCategory classifies failures without carrying response bodies, which
// may echo request data.
type ErrorCategory string

const (
	ErrorValidation       ErrorCategory = "validation"
	ErrorAuthentication   ErrorCategory = "authentication"
	ErrorPermission       ErrorCategory = "permission"
	ErrorNotFound         ErrorCategory = "not_found"
	ErrorApprovalRequired ErrorCategory = "approval_required"
	ErrorReasonRequired   ErrorCategory = "reason_required"
	ErrorMFARequired      ErrorCategory = "mfa_required"
	ErrorUnavailable      ErrorCategory = "unavailable"
	ErrorTimeout          ErrorCategory = "timeout"
	ErrorResponseInvalid  ErrorCategory = "response_invalid"
	ErrorTransport        ErrorCategory = "transport"
)

// Error is a categorized failure. Name is the Infisical error name, which is a
// fixed identifier and safe to log.
type Error struct {
	Category  ErrorCategory
	Operation string
	Status    int
	Name      string
	cause     error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("infisical pam %s failed: %s", e.Operation, e.Category)
	if e.Status != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.Status)
	}
	if e.Name != "" {
		message += " [" + e.Name + "]"
	}
	// Validation causes are fixed texts from this package; other causes can
	// carry remote response or network detail and stay out of the message.
	if e.Category == ErrorValidation && e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.cause }

func newError(category ErrorCategory, operation string, cause error) *Error {
	return &Error{Category: category, Operation: operation, cause: cause}
}

// CategoryOf returns the category of err, or "" when err is not an *Error.
func CategoryOf(err error) ErrorCategory {
	var pamErr *Error
	if errors.As(err, &pamErr) {
		return pamErr.Category
	}
	return ""
}

// Config describes one Infisical instance and machine identity.
type Config struct {
	// SiteURL is the instance root, e.g. https://app.infisical.com. The API is
	// served below /api.
	SiteURL string
	// CACertificate optionally adds PEM roots for the API endpoint.
	CACertificate []byte
	ClientID      string
	// ClientSecret is the universal-auth client secret. The caller owns and
	// zeroes it.
	ClientSecret []byte
	// OrganizationSlug scopes the login to a sub-organization when set.
	OrganizationSlug string
	Timeout          time.Duration
}

var (
	clientIDPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,127}$`)
	organizationSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	accountPathSegment      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.-]{0,127}$`)
	sessionIDPattern        = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)
	errorNamePattern        = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
)

// ValidateSiteURL accepts an absolute https URL without credentials, query or
// fragment.
func ValidateSiteURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, newError(ErrorValidation, "configure", errors.New("site URL must be an https URL"))
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed, nil
}

// ValidateAccountPath accepts folder/account paths as used by `infisical pam
// access`, with at least one folder segment.
func ValidateAccountPath(path string) error {
	segments := strings.Split(path, "/")
	if len(path) > 512 || len(segments) < 2 {
		return newError(ErrorValidation, "configure", errors.New("account path must be folder/account"))
	}
	for _, segment := range segments {
		if !accountPathSegment.MatchString(segment) || strings.TrimSpace(segment) != segment {
			return newError(ErrorValidation, "configure", errors.New("account path is invalid"))
		}
	}
	return nil
}

func (c Config) validate() (*url.URL, error) {
	base, err := ValidateSiteURL(c.SiteURL)
	if err != nil {
		return nil, err
	}
	if !clientIDPattern.MatchString(c.ClientID) {
		return nil, newError(ErrorValidation, "configure", errors.New("client id is invalid"))
	}
	if len(c.ClientSecret) == 0 || len(c.ClientSecret) > 4096 {
		return nil, newError(ErrorValidation, "configure", errors.New("client secret is required"))
	}
	if c.OrganizationSlug != "" && !organizationSlugPattern.MatchString(c.OrganizationSlug) {
		return nil, newError(ErrorValidation, "configure", errors.New("organization slug is invalid"))
	}
	if c.Timeout < 0 || c.Timeout > MaximumTimeout {
		return nil, newError(ErrorValidation, "configure", errors.New("timeout is out of range"))
	}
	return base, nil
}

// Client talks to the Infisical API for one machine identity.
type Client struct {
	config Config
	base   *url.URL
	http   *http.Client
}

func NewClient(config Config) (*Client, error) {
	base, err := config.validate()
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(config.CACertificate) > 0 {
		pool, poolErr := x509.SystemCertPool()
		if poolErr != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(config.CACertificate) {
			return nil, newError(ErrorValidation, "configure", errors.New("CA certificate is invalid"))
		}
		tlsConfig.RootCAs = pool
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	return &Client{
		config: config,
		base:   base,
		http: &http.Client{
			Timeout:   timeout,
			Transport: transport,
			// A redirect would carry the bearer token or client secret to a
			// location the operator did not configure.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// AccessToken is a machine identity token. It is held in memory only.
type AccessToken struct {
	Value     string
	ExpiresAt time.Time
}

type universalAuthLoginRequest struct {
	ClientID         string `json:"clientId"`
	ClientSecret     string `json:"clientSecret"`
	OrganizationSlug string `json:"organizationSlug,omitempty"`
}

type universalAuthLoginResponse struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int    `json:"expiresIn"`
	TokenType   string `json:"tokenType"`
}

// Login exchanges the universal-auth credentials for an access token.
func (c *Client) Login(ctx context.Context) (AccessToken, error) {
	request := universalAuthLoginRequest{
		ClientID:         c.config.ClientID,
		ClientSecret:     string(c.config.ClientSecret),
		OrganizationSlug: c.config.OrganizationSlug,
	}
	var response universalAuthLoginResponse
	if err := c.post(ctx, "login", "/api/v1/auth/universal-auth/login", "", request, &response); err != nil {
		return AccessToken{}, err
	}
	if response.AccessToken == "" || len(response.AccessToken) > 16384 ||
		(response.TokenType != "" && !strings.EqualFold(response.TokenType, "Bearer")) {
		return AccessToken{}, newError(ErrorResponseInvalid, "login", nil)
	}
	token := AccessToken{Value: response.AccessToken}
	if response.ExpiresIn > 0 {
		token.ExpiresAt = time.Now().Add(time.Duration(response.ExpiresIn) * time.Second)
	}
	return token, nil
}

// AccessRequest opens one PAM session for one account.
type AccessRequest struct {
	Path     string
	Duration time.Duration
	Reason   string
}

func (r AccessRequest) validate() error {
	if err := ValidateAccountPath(r.Path); err != nil {
		return err
	}
	if r.Duration < time.Minute || r.Duration > MaximumSessionDuration {
		return newError(ErrorValidation, "access", errors.New("session duration is out of range"))
	}
	if len(r.Reason) > maximumReasonLength || strings.ContainsAny(r.Reason, "\r\n\x00") {
		return newError(ErrorValidation, "access", errors.New("reason is invalid"))
	}
	return nil
}

type accessRequestBody struct {
	Path     string `json:"path"`
	Duration string `json:"duration"`
	Reason   string `json:"reason,omitempty"`
}

type accessResponseBody struct {
	SessionID                     string `json:"sessionId"`
	AccountType                   string `json:"accountType"`
	GatewayID                     string `json:"gatewayId"`
	RelayHost                     string `json:"relayHost"`
	DirectAddress                 string `json:"directAddress"`
	RelayClientCertificate        string `json:"relayClientCertificate"`
	RelayClientPrivateKey         string `json:"relayClientPrivateKey"`
	RelayServerCertificateChain   string `json:"relayServerCertificateChain"`
	GatewayClientCertificate      string `json:"gatewayClientCertificate"`
	GatewayClientPrivateKey       string `json:"gatewayClientPrivateKey"`
	GatewayServerCertificateChain string `json:"gatewayServerCertificateChain"`
}

// Access opens a PAM session. Only SSH accounts are accepted.
func (c *Client) Access(ctx context.Context, token AccessToken, request AccessRequest) (*Session, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}
	body := accessRequestBody{
		Path:     request.Path,
		Duration: request.Duration.String(),
		Reason:   request.Reason,
	}
	var response accessResponseBody
	if err := c.post(ctx, "access", "/api/v1/pam/accounts/access", token.Value, body, &response); err != nil {
		return nil, err
	}
	var failure error
	if response.AccountType != "" && response.AccountType != "ssh" {
		failure = newError(ErrorValidation, "access", errors.New("account is not an SSH account"))
	}
	session, err := newSession(response, time.Now().Add(request.Duration))
	if failure == nil && err != nil {
		failure = newError(ErrorResponseInvalid, "access", err)
	}
	if failure != nil {
		// Infisical opened the session even though it is unusable here; end
		// it rather than leave it running until it expires.
		if sessionIDPattern.MatchString(response.SessionID) {
			_ = c.EndSession(ctx, token, response.SessionID)
		}
		return nil, failure
	}
	return session, nil
}

// EndSession ends a session through the API.
func (c *Client) EndSession(ctx context.Context, token AccessToken, sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return newError(ErrorValidation, "end", errors.New("session id is invalid"))
	}
	return c.post(ctx, "end", "/api/v1/pam/sessions/"+url.PathEscape(sessionID)+"/end", token.Value, nil, nil)
}

type errorBody struct {
	Name string `json:"error"`
}

func (c *Client) post(ctx context.Context, operation, path, bearer string, body, result any) error {
	endpoint := *c.base
	endpoint.Path = c.base.Path + path
	payload := []byte("{}")
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return newError(ErrorValidation, operation, err)
		}
		payload = encoded
	}
	defer zero(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return newError(ErrorValidation, operation, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil || isTimeout(err) {
			return newError(ErrorTimeout, operation, err)
		}
		return newError(ErrorUnavailable, operation, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximumResponseSize+1))
	if err != nil {
		return newError(ErrorUnavailable, operation, err)
	}
	defer zero(data)
	if int64(len(data)) > maximumResponseSize {
		return newError(ErrorResponseInvalid, operation, errors.New("response too large"))
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return statusError(operation, response.StatusCode, data)
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(data, result); err != nil {
		return newError(ErrorResponseInvalid, operation, err)
	}
	return nil
}

func statusError(operation string, status int, data []byte) error {
	var parsed errorBody
	_ = json.Unmarshal(data, &parsed)
	name := ""
	if errorNamePattern.MatchString(parsed.Name) {
		name = parsed.Name
	}
	category := ErrorUnavailable
	switch {
	case name == "PAM_APPROVAL_REQUIRED" || name == "PAM_GRANT_EXPIRED":
		category = ErrorApprovalRequired
	case name == "PAM_REASON_REQUIRED":
		category = ErrorReasonRequired
	case name == "SESSION_MFA_REQUIRED":
		category = ErrorMFARequired
	case status == http.StatusUnauthorized:
		category = ErrorAuthentication
	case status == http.StatusForbidden:
		category = ErrorPermission
	case status == http.StatusNotFound:
		category = ErrorNotFound
	case status == http.StatusBadRequest || status == http.StatusUnprocessableEntity:
		category = ErrorValidation
	case status >= 300 && status < 400:
		category = ErrorResponseInvalid
	}
	return &Error{Category: category, Operation: operation, Status: status, Name: name}
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func zero(data []byte) {
	for index := range data {
		data[index] = 0
	}
}
