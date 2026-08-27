package pro_interfaces

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const CapabilityRuntimeSecrets CapabilityID = "runtime_secrets"

type SecretProviderType string

const (
	SecretProviderVault   SecretProviderType = "vault"
	SecretProviderOpenBao SecretProviderType = "openbao"
)

type SecretProviderAuthMethod string

const (
	SecretProviderAuthToken      SecretProviderAuthMethod = "token"
	SecretProviderAuthAppRole    SecretProviderAuthMethod = "approle"
	SecretProviderAuthKubernetes SecretProviderAuthMethod = "kubernetes"
)

var (
	secretReferenceMountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	secretReferenceFieldPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	secretReferencePathSegment  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:@+-]{0,127}$`)
)

// SecretReference is the value-free, provider-neutral identity persisted for
// one remote secret field. Version zero selects the provider's latest version.
type SecretReference struct {
	StorageID int    `json:"storage_id"`
	Mount     string `json:"mount"`
	Path      string `json:"path"`
	Version   int    `json:"version,omitempty"`
	Field     string `json:"field"`
}

func (r SecretReference) Validate() error {
	if r.StorageID <= 0 {
		return errors.New("secret storage id must be positive")
	}
	if !secretReferenceMountPattern.MatchString(r.Mount) {
		return errors.New("secret mount is invalid")
	}
	if len(r.Path) == 0 || len(r.Path) > 512 || strings.HasPrefix(r.Path, "/") || strings.HasSuffix(r.Path, "/") {
		return errors.New("secret path is invalid")
	}
	for _, segment := range strings.Split(r.Path, "/") {
		if segment == "." || segment == ".." || !secretReferencePathSegment.MatchString(segment) {
			return errors.New("secret path is invalid")
		}
	}
	if r.Version < 0 {
		return errors.New("secret version must not be negative")
	}
	if !secretReferenceFieldPattern.MatchString(r.Field) {
		return errors.New("secret field is invalid")
	}
	return nil
}

func (r SecretReference) Encode() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(r)
	return string(encoded), err
}

func DecodeSecretReference(encoded string) (SecretReference, error) {
	var reference SecretReference
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&reference); err != nil {
		return SecretReference{}, errors.New("secret reference is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SecretReference{}, errors.New("secret reference is invalid")
	}
	if err := reference.Validate(); err != nil {
		return SecretReference{}, err
	}
	return reference, nil
}

// Fingerprint is safe to persist and audit because it is derived exclusively
// from reference metadata and never from a resolved value.
func (r SecretReference) Fingerprint() string {
	encoded, err := r.Encode()
	if err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(encoded))
	return hex.EncodeToString(sum[:])
}

type SecretProviderAuth struct {
	Method              SecretProviderAuthMethod
	Mount               string
	RoleID              string
	Role                string
	BootstrapCredential []byte
}

type SecretProviderConfiguration struct {
	StorageID       int
	ProjectID       int
	Type            SecretProviderType
	Address         string
	Namespace       string
	DefaultMount    string
	CACertificate   []byte
	Timeout         time.Duration
	MaxResponseSize int64
	Auth            SecretProviderAuth
}

type SecretProviderHealthState string

const (
	SecretProviderHealthUnknown SecretProviderHealthState = "unknown"
	SecretProviderHealthHealthy SecretProviderHealthState = "healthy"
	SecretProviderHealthFailed  SecretProviderHealthState = "failed"
)

type SecretProviderErrorCategory string

const (
	SecretProviderErrorValidation         SecretProviderErrorCategory = "validation"
	SecretProviderErrorCapabilityDisabled SecretProviderErrorCategory = "capability_disabled"
	SecretProviderErrorAuthentication     SecretProviderErrorCategory = "authentication"
	SecretProviderErrorPermission         SecretProviderErrorCategory = "permission"
	SecretProviderErrorTLS                SecretProviderErrorCategory = "tls"
	SecretProviderErrorTimeout            SecretProviderErrorCategory = "timeout"
	SecretProviderErrorUnavailable        SecretProviderErrorCategory = "unavailable"
	SecretProviderErrorResponseInvalid    SecretProviderErrorCategory = "response_invalid"
	SecretProviderErrorResponseTooLarge   SecretProviderErrorCategory = "response_too_large"
	SecretProviderErrorFieldMissing       SecretProviderErrorCategory = "field_missing"
)

type SecretProviderError struct {
	Category  SecretProviderErrorCategory
	Operation string
}

func (e SecretProviderError) Error() string {
	return fmt.Sprintf("runtime secret %s failed: %s", e.Operation, e.Category)
}

type SecretProviderHealth struct {
	StorageID      int                         `json:"storage_id"`
	State          SecretProviderHealthState   `json:"state"`
	ErrorCategory  SecretProviderErrorCategory `json:"error_category,omitempty"`
	CheckedAt      time.Time                   `json:"checked_at"`
	LatencyMillis  int64                       `json:"latency_ms"`
	TokenExpiresAt *time.Time                  `json:"token_expires_at,omitempty"`
	TokenRenewable bool                        `json:"token_renewable"`
}

// VaultOpenBaoClient is the outbound provider port. Implementations own auth
// exchange and token renewal; returned secret values must not be cached.
type VaultOpenBaoClient interface {
	ReadKV(context.Context, SecretProviderConfiguration, SecretReference) ([]byte, error)
	TestConnection(context.Context, SecretProviderConfiguration) (SecretProviderHealth, error)
}

type RuntimeSecretResolver interface {
	ResolveRuntimeSecret(context.Context, int, SecretReference) ([]byte, error)
	TestRuntimeSecretProvider(context.Context, int, int) (SecretProviderHealth, error)
}
