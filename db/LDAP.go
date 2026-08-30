package db

import "time"

type LDAPProvider struct {
	ID                     string     `db:"id"`
	DisplayName            string     `db:"display_name"`
	State                  string     `db:"state"`
	ServerURL              string     `db:"server_url"`
	TLSMode                string     `db:"tls_mode"`
	TrustMode              string     `db:"trust_mode"`
	CAPEM                  string     `db:"ca_pem"`
	BindDN                 string     `db:"bind_dn"`
	EncryptedBindPassword  string     `db:"encrypted_bind_password"`
	ConfigVersion          int        `db:"config_version"`
	SearchBaseDN           string     `db:"search_base_dn"`
	UserFilter             string     `db:"user_filter"`
	IdentityAttribute      string     `db:"identity_attribute"`
	UsernameAttribute      string     `db:"username_attribute"`
	NameAttribute          string     `db:"name_attribute"`
	EmailAttribute         string     `db:"email_attribute"`
	GroupSearchBaseDN      string     `db:"group_search_base_dn"`
	GroupUserFilter        string     `db:"group_user_filter"`
	GroupFilter            string     `db:"group_filter"`
	GroupIdentityAttribute string     `db:"group_identity_attribute"`
	GroupMemberAttribute   string     `db:"group_member_attribute"`
	GroupMaxDepth          int        `db:"group_max_depth"`
	ReadinessStatus        string     `db:"readiness_status"`
	ReadinessCode          string     `db:"readiness_code"`
	ReadinessCheckedAt     *time.Time `db:"readiness_checked_at"`
	RecoveryAdminUserID    *int       `db:"recovery_admin_user_id"`
	RecoveryCheckedAt      *time.Time `db:"recovery_checked_at"`
	Created                time.Time  `db:"created"`
	Updated                time.Time  `db:"updated"`
}

type LDAPGroupMapping struct {
	ID              string    `db:"id" json:"id"`
	ProviderID      string    `db:"provider_id" json:"provider_id"`
	GroupExternalID string    `db:"group_external_id" json:"group_external_id"`
	TargetScope     string    `db:"target_scope" json:"target_scope"`
	ProjectID       *int      `db:"project_id" json:"project_id,omitempty"`
	RoleID          string    `db:"role_id" json:"role_id"`
	Enabled         bool      `db:"enabled" json:"enabled"`
	Revision        int       `db:"revision" json:"revision"`
	Created         time.Time `db:"created" json:"created"`
	Updated         time.Time `db:"updated" json:"updated"`
}

type LDAPLinkedUser struct {
	ExternalID string `db:"external_uid"`
	UserID     int    `db:"user_id"`
}

type LDAPGroupRoleAssignment struct {
	UserID                 int    `db:"user_id"`
	TargetScope            string `db:"target_scope"`
	ProjectID              *int   `db:"project_id"`
	RoleID                 string `db:"role_id"`
	ManagedByMappingID     string `db:"managed_by_mapping_id"`
	ProtectedAdministrator bool   `db:"protected_administrator"`
}

type LDAPGroupAssignmentChange struct {
	MappingID   string
	UserID      int
	TargetScope string
	ProjectID   *int
	RoleID      string
}

type LDAPGroupReconciliation struct {
	ID                  int        `db:"id" json:"id"`
	ProviderID          string     `db:"provider_id" json:"provider_id"`
	Source              string     `db:"source" json:"source"`
	Status              string     `db:"status" json:"status"`
	Token               string     `db:"token" json:"token"`
	MappingRevision     int        `db:"mapping_revision" json:"mapping_revision"`
	DirectoryRevision   string     `db:"directory_revision" json:"directory_revision"`
	PreviewJSON         string     `db:"preview_json" json:"-"`
	AdditionCount       int        `db:"addition_count" json:"addition_count"`
	RemovalCount        int        `db:"removal_count" json:"removal_count"`
	UnresolvedCount     int        `db:"unresolved_count" json:"unresolved_count"`
	CollisionCount      int        `db:"collision_count" json:"collision_count"`
	ProtectedAdminCount int        `db:"protected_admin_count" json:"protected_admin_count"`
	ErrorCode           string     `db:"error_code" json:"error_code,omitempty"`
	ActorID             *int       `db:"actor_id" json:"actor_id,omitempty"`
	Created             time.Time  `db:"created" json:"created"`
	AppliedAt           *time.Time `db:"applied_at" json:"applied_at,omitempty"`
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
