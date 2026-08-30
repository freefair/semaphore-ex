package db

import "time"

type OIDCGroupMapping struct {
	ID          string    `db:"id" json:"id"`
	ProviderID  string    `db:"provider_id" json:"provider_id"`
	ClaimValue  string    `db:"claim_value" json:"claim_value"`
	TargetScope string    `db:"target_scope" json:"target_scope"`
	ProjectID   *int      `db:"project_id" json:"project_id,omitempty"`
	RoleID      string    `db:"role_id" json:"role_id"`
	Enabled     bool      `db:"enabled" json:"enabled"`
	Revision    int       `db:"revision" json:"revision"`
	Created     time.Time `db:"created" json:"created"`
	Updated     time.Time `db:"updated" json:"updated"`
}

type OIDCGroupRoleAssignment struct {
	UserID                 int    `db:"user_id"`
	TargetScope            string `db:"target_scope"`
	ProjectID              *int   `db:"project_id"`
	RoleID                 string `db:"role_id"`
	OwnerKind              string `db:"owner_kind"`
	ManagedByProviderID    string `db:"managed_by_provider_id"`
	ManagedByMappingID     string `db:"managed_by_mapping_id"`
	ProtectedAdministrator bool   `db:"protected_administrator"`
}

type OIDCGroupAssignmentChange struct {
	MappingID   string
	UserID      int
	TargetScope string
	ProjectID   *int
	RoleID      string
}

type OIDCGroupReconciliation struct {
	ID                  int        `db:"id" json:"id"`
	ProviderID          string     `db:"provider_id" json:"provider_id"`
	UserID              int        `db:"user_id" json:"user_id"`
	Source              string     `db:"source" json:"source"`
	Status              string     `db:"status" json:"status"`
	Token               string     `db:"token" json:"token"`
	MappingRevision     int        `db:"mapping_revision" json:"mapping_revision"`
	ClaimRevision       string     `db:"claim_revision" json:"claim_revision"`
	PreviewJSON         string     `db:"preview_json" json:"-"`
	AdditionCount       int        `db:"addition_count" json:"addition_count"`
	RemovalCount        int        `db:"removal_count" json:"removal_count"`
	UnknownCount        int        `db:"unknown_count" json:"unknown_count"`
	CollisionCount      int        `db:"collision_count" json:"collision_count"`
	ProtectedAdminCount int        `db:"protected_admin_count" json:"protected_admin_count"`
	ErrorCode           string     `db:"error_code" json:"error_code,omitempty"`
	ActorID             *int       `db:"actor_id" json:"actor_id,omitempty"`
	Created             time.Time  `db:"created" json:"created"`
	AppliedAt           *time.Time `db:"applied_at" json:"applied_at,omitempty"`
}
