package jwt

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Audience encodes the JWT "aud" claim. Per RFC 7519 §4.1.3 the value may be
// either a single string or a JSON array of strings. If we have only one audience,
// we encode it as a string, otherwise as an array. An empty audience is encoded as JSON null.
type Audience []string

// MarshalJSON implements json.Marshaler.
func (a Audience) MarshalJSON() ([]byte, error) {
	switch len(a) {
	case 0:
		return []byte("null"), nil
	case 1:
		return json.Marshal(a[0])
	default:
		return json.Marshal([]string(a))
	}
}

// UnmarshalJSON accepts only the RFC 7519 audience shapes: a string, an array
// of strings, or null. In particular, null array elements must not silently
// become empty strings because that would bypass the claim's JSON contract.
func (a *Audience) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if bytes.Equal(data, []byte("null")) {
		*a = nil
		return nil
	}

	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = Audience{single}
		return nil
	}

	var values []json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("JWT audience must be a string, an array of strings, or null")
	}
	result := make(Audience, len(values))
	for index, value := range values {
		if len(value) == 0 || bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("JWT audience array entries must be strings")
		}
		if err := json.Unmarshal(value, &result[index]); err != nil {
			return fmt.Errorf("JWT audience array entries must be strings")
		}
	}
	*a = result
	return nil
}

// IsZero lets `omitempty` skip an empty audience claim.
func (a Audience) IsZero() bool { return len(a) == 0 }

// TaskClaims is the JWT payload issued by Semaphore for a single task run.
type TaskClaims struct {
	// Registered claims
	Issuer    string   `json:"iss,omitempty"`
	Subject   string   `json:"sub,omitempty"`
	Audience  Audience `json:"aud,omitempty"`
	ExpiresAt int64    `json:"exp,omitempty"`
	NotBefore int64    `json:"nbf,omitempty"`
	IssuedAt  int64    `json:"iat,omitempty"`
	JWTID     string   `json:"jti,omitempty"`

	// Semaphore-specific claims
	TaskID     int  `json:"task_id"`
	ProjectID  int  `json:"project_id"`
	TemplateID int  `json:"template_id"`
	UserID     *int `json:"user_id,omitempty"`
}

// TaskInfo bundles the data needed to mint a TaskClaims set.
type TaskInfo struct {
	TaskID     int
	ProjectID  int
	TemplateID int
	UserID     *int

	Audience Audience
	TTL      time.Duration
}

// SignerOptions controls token issuance defaults.
type SignerOptions struct {
	Issuer     string
	DefaultTTL time.Duration
	MaxTTL     time.Duration
}
