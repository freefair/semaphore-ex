package pro_interfaces

import (
	"github.com/semaphoreui/semaphore/db"
)

// WorkflowApprovalIdentityStore resolves the current project role used to
// authorize an approval decision. The request itself snapshots the required
// permission, so later definition edits cannot weaken the pending request.
type WorkflowApprovalIdentityStore interface {
	GetProjectUser(projectID int, userID int) (db.ProjectUser, error)
	GetProjectOrGlobalRoleBySlug(projectID int, slug string) (db.Role, error)
}

// WorkflowCredentialReader resolves an approved AccessKey immediately before
// a workflow task is created. Implementations must keep the value write-only.
type WorkflowCredentialReader interface {
	DeserializeSecret(key *db.AccessKey) error
}
