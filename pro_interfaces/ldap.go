package pro_interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/semaphoreui/semaphore/db"
)

// LDAPState is the administrator-controlled exposure state of one provider.
type LDAPState string

const (
	LDAPStateDisabled      LDAPState = "disabled"
	LDAPStateShadow        LDAPState = "shadow"
	LDAPStateSelectedUsers LDAPState = "selected_users"
	LDAPStateActive        LDAPState = "active"
)

type LDAPTLSMode string

const (
	LDAPTLSModeLDAPS    LDAPTLSMode = "ldaps"
	LDAPTLSModeStartTLS LDAPTLSMode = "starttls"
)

type LDAPTrustMode string

const (
	LDAPTrustModeSystem LDAPTrustMode = "system"
	LDAPTrustModeCustom LDAPTrustMode = "custom_ca"
)

type LDAPReadinessStatus string

const (
	LDAPReadinessUntested LDAPReadinessStatus = "untested"
	LDAPReadinessReady    LDAPReadinessStatus = "ready"
	LDAPReadinessFailed   LDAPReadinessStatus = "failed"
)

type LDAPReadiness struct {
	Status     LDAPReadinessStatus `json:"status"`
	Connection bool                `json:"connection"`
	Search     bool                `json:"search"`
	Bind       bool                `json:"bind"`
	Recovery   bool                `json:"recovery"`
	Code       string              `json:"code,omitempty"`
	CheckedAt  *time.Time          `json:"checked_at,omitempty"`
}

// LDAPProviderConfiguration is the secret-free administrative view.
type LDAPProviderConfiguration struct {
	ID                     string        `json:"id"`
	DisplayName            string        `json:"display_name"`
	State                  LDAPState     `json:"state"`
	ServerURL              string        `json:"server_url"`
	TLSMode                LDAPTLSMode   `json:"tls_mode"`
	TrustMode              LDAPTrustMode `json:"trust_mode"`
	CAPEM                  string        `json:"ca_pem,omitempty"`
	BindDN                 string        `json:"bind_dn"`
	BindPasswordConfigured bool          `json:"bind_password_configured"`
	SearchBaseDN           string        `json:"search_base_dn"`
	UserFilter             string        `json:"user_filter"`
	IdentityAttribute      string        `json:"identity_attribute"`
	UsernameAttribute      string        `json:"username_attribute"`
	NameAttribute          string        `json:"name_attribute"`
	EmailAttribute         string        `json:"email_attribute"`
	SelectedUserIDs        []int         `json:"selected_user_ids"`
	EligibleUserIDs        []int         `json:"eligible_user_ids"`
	RecoveryAdminUserID    *int          `json:"recovery_admin_user_id,omitempty"`
	Readiness              LDAPReadiness `json:"readiness"`
	Created                time.Time     `json:"created"`
	Updated                time.Time     `json:"updated"`
}

// LDAPProviderInput contains write-only secrets accepted by Configure.
type LDAPProviderInput struct {
	ID                string
	DisplayName       string
	ServerURL         string
	TLSMode           LDAPTLSMode
	TrustMode         LDAPTrustMode
	CAPEM             string
	BindDN            string
	BindPassword      string
	SearchBaseDN      string
	UserFilter        string
	IdentityAttribute string
	UsernameAttribute string
	NameAttribute     string
	EmailAttribute    string
}

type LDAPConfigureRequest struct {
	ActorID      int
	ActorIsAdmin bool
	Provider     LDAPProviderInput
	Now          time.Time
}

type LDAPTestRequest struct {
	ActorID               int
	ActorIsAdmin          bool
	ProviderID            string
	Username              string
	Password              string
	RecoveryAdminUserID   int
	RecoveryAdminPassword string
	Now                   time.Time
}

type LDAPStateRequest struct {
	ActorID         int
	ActorIsAdmin    bool
	ProviderID      string
	State           LDAPState
	SelectedUserIDs []int
	Now             time.Time
}

type LDAPAuthenticationRequest struct {
	ProviderID string
	Username   string
	Password   string
	Now        time.Time
}

type LDAPLinkRequest struct {
	ActorID    int
	ProviderID string
	Username   string
	Password   string
	Now        time.Time
}

type LDAPLoginProvider struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	State       LDAPState `json:"state"`
	Unavailable bool      `json:"unavailable,omitempty"`
}

// LDAPClientConfiguration contains decrypted material only for one outbound call.
type LDAPClientConfiguration struct {
	ServerURL         string
	TLSMode           LDAPTLSMode
	TrustMode         LDAPTrustMode
	CAPEM             string
	BindDN            string
	BindPassword      string
	SearchBaseDN      string
	UserFilter        string
	IdentityAttribute string
	UsernameAttribute string
	NameAttribute     string
	EmailAttribute    string
}

type LDAPClientRequest struct {
	Configuration LDAPClientConfiguration
	Username      string
	Credential    string
}

func NewLDAPClientRequest(
	configuration LDAPClientConfiguration,
	username string,
	credential string,
) LDAPClientRequest {
	return LDAPClientRequest{configuration, username, credential}
}

type LDAPIdentity struct {
	ExternalID string
	Username   string
	Name       string
	Email      string
}

type LDAPClientResult struct {
	Identity  LDAPIdentity
	Readiness LDAPReadiness
}

// LegacyLDAPClientConfiguration preserves the Community configuration shape
// while keeping its protocol behavior outside the HTTP layer.
type LegacyLDAPClientConfiguration struct {
	Server        string
	TLS           bool
	TLSSkipVerify bool
	BindDN        string
	BindPassword  string
	SearchBaseDN  string
	SearchFilter  string
	Attributes    []string
}

type LegacyLDAPClientRequest struct {
	Configuration LegacyLDAPClientConfiguration
	Username      string
	Credential    string
}

type LegacyLDAPClientResult struct {
	ExternalID string
	Attributes map[string]any
}

// LegacyLDAPClient is the compatibility-only Community transport boundary.
type LegacyLDAPClient interface {
	Authenticate(context.Context, LegacyLDAPClientRequest) (*LegacyLDAPClientResult, error)
}

// LDAPClient is the outbound protocol boundary. Implementations own all LDAP
// transport, TLS, filter, search, referral, and bind behavior.
type LDAPClient interface {
	Validate(LDAPClientConfiguration) error
	Authenticate(context.Context, LDAPClientRequest) (LDAPClientResult, error)
}

// LDAPService is the framework-free enhanced LDAP identity boundary.
type LDAPService interface {
	Initialize(context.Context) error
	LoginProviders(context.Context) ([]LDAPLoginProvider, error)
	AllowLocalRecovery(context.Context, string) (bool, error)
	Authenticate(context.Context, LDAPAuthenticationRequest) (db.User, error)
	Link(context.Context, LDAPLinkRequest) error
	Providers(context.Context) ([]LDAPProviderConfiguration, error)
	Configure(context.Context, LDAPConfigureRequest) (LDAPProviderConfiguration, error)
	Test(context.Context, LDAPTestRequest) (LDAPReadiness, error)
	SetState(context.Context, LDAPStateRequest) (LDAPProviderConfiguration, error)
	Transitions(context.Context, string) ([]db.LDAPCapabilityTransition, error)
}

var (
	ErrLDAPUnavailable                     = errors.New("LDAP capability unavailable")
	ErrLDAPDisabled                        = errors.New("LDAP provider disabled")
	ErrLDAPProviderNotFound                = errors.New("LDAP provider not found")
	ErrLDAPInvalidCredentials              = errors.New("invalid LDAP credentials")
	ErrLDAPProviderUnavailable             = errors.New("LDAP provider unavailable")
	ErrLDAPThrottled                       = errors.New("LDAP authentication throttled")
	ErrLDAPIdentityCollision               = errors.New("LDAP identity collision")
	ErrLDAPReadiness                       = errors.New("LDAP provider readiness check failed")
	ErrLDAPReconfigurationRequiresInactive = errors.New("LDAP provider must be inactive before reconfiguration")
	ErrLDAPForbidden                       = errors.New("LDAP operation forbidden")
	ErrLDAPReferral                        = errors.New("LDAP referral rejected")
	ErrLDAPDuplicateIdentity               = errors.New("LDAP search returned duplicate identities")
)
