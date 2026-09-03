package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
)

func TestAuthorizeWorkflowFileArtifactAccessNarrowsCurrentRole(t *testing.T) {
	known := db.KnownProjectRoleReferences(nil)
	owner := db.ProjectWorkflowRoleIdentity{
		Reference: db.BuiltinProjectRoleReferenceOwner, Permissions: db.CanViewWorkflows,
		Revision: 1, Origin: db.ProjectWorkflowRoleOriginBuiltIn,
	}
	manager := db.ProjectWorkflowRoleIdentity{
		Reference: db.BuiltinProjectRoleReferenceManager, Permissions: db.CanViewWorkflows,
		Revision: 1, Origin: db.ProjectWorkflowRoleOriginBuiltIn,
	}
	policy := db.WorkflowFileArtifactAccessPolicy{
		Revision: 1, RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
	}
	if !AuthorizeWorkflowFileArtifactAccess(policy, owner, known) {
		t.Fatal("allowed current role rejected")
	}
	if AuthorizeWorkflowFileArtifactAccess(policy, manager, known) {
		t.Fatal("artifact role narrowing ignored")
	}
	if !AuthorizeWorkflowFileArtifactAccess(db.WorkflowFileArtifactAccessPolicy{Revision: 1}, manager, known) {
		t.Fatal("empty artifact role policy must preserve workflow access")
	}

	unknownCustom := db.ProjectRoleReferenceForCustomRole("removed")
	owner.Reference = unknownCustom
	owner.Origin = db.ProjectWorkflowRoleOriginManual
	policy.RoleIDs = []db.ProjectRoleReference{unknownCustom}
	if AuthorizeWorkflowFileArtifactAccess(policy, owner, known) {
		t.Fatal("removed custom role remained authorized")
	}
}
