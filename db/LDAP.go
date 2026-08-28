package db

import "time"

type LDAPProvider struct {
	ID                    string     `db:"id"`
	DisplayName           string     `db:"display_name"`
	State                 string     `db:"state"`
	ServerURL             string     `db:"server_url"`
	TLSMode               string     `db:"tls_mode"`
	TrustMode             string     `db:"trust_mode"`
	CAPEM                 string     `db:"ca_pem"`
	BindDN                string     `db:"bind_dn"`
	EncryptedBindPassword string     `db:"encrypted_bind_password"`
	ConfigVersion         int        `db:"config_version"`
	SearchBaseDN          string     `db:"search_base_dn"`
	UserFilter            string     `db:"user_filter"`
	IdentityAttribute     string     `db:"identity_attribute"`
	UsernameAttribute     string     `db:"username_attribute"`
	NameAttribute         string     `db:"name_attribute"`
	EmailAttribute        string     `db:"email_attribute"`
	ReadinessStatus       string     `db:"readiness_status"`
	ReadinessCode         string     `db:"readiness_code"`
	ReadinessCheckedAt    *time.Time `db:"readiness_checked_at"`
	RecoveryAdminUserID   *int       `db:"recovery_admin_user_id"`
	RecoveryCheckedAt     *time.Time `db:"recovery_checked_at"`
	Created               time.Time  `db:"created"`
	Updated               time.Time  `db:"updated"`
}

type LDAPAuthAttempt struct {
	ProviderID    string     `db:"provider_id"`
	SubjectHash   string     `db:"subject_hash"`
	FailureCount  int        `db:"failure_count"`
	WindowStarted time.Time  `db:"window_started"`
	BlockedUntil  *time.Time `db:"blocked_until"`
	Updated       time.Time  `db:"updated"`
}

type LDAPCapabilityTransition struct {
	ID         int       `db:"id" json:"id"`
	ProviderID string    `db:"provider_id" json:"provider_id"`
	FromState  string    `db:"from_state" json:"from_state"`
	ToState    string    `db:"to_state" json:"to_state"`
	ActorID    int       `db:"actor_id" json:"actor_id"`
	Created    time.Time `db:"created" json:"created"`
}
