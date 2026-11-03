package pro_interfaces

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type objectGrantIdentityStore struct{ grant db.ProjectUserPermission }

func (s *objectGrantIdentityStore) ResolveProjectWorkflowRoleIdentity(projectID, userID int) (db.ProjectWorkflowRoleIdentity, error) {
	return db.ProjectWorkflowRoleIdentity{Reference: db.BuiltinProjectRoleReferenceGuest, Revision: 1, Origin: db.ProjectWorkflowRoleOriginBuiltIn}, nil
}
func (s *objectGrantIdentityStore) ResolveProjectWorkflowRoleIdentities(projectID, userID int) ([]db.ProjectWorkflowRoleIdentity, error) {
	identity, err := s.ResolveProjectWorkflowRoleIdentity(projectID, userID)
	return []db.ProjectWorkflowRoleIdentity{identity}, err
}
func (s *objectGrantIdentityStore) GetProjectRoles(projectID int) ([]db.Role, error) { return nil, nil }
func (s *objectGrantIdentityStore) GetWorkflowPermission(projectID, workflowID, userID int) (db.ProjectUserPermission, error) {
	return s.grant, nil
}

func TestWorkflowObjectGrantStillRequiresCurrentRolePolicy(t *testing.T) {
	store := &objectGrantIdentityStore{grant: db.CanReadProject | db.CanStartWorkflows}
	workflow := db.WorkflowTemplate{ID: 7, ProjectID: 2, AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceGuest}}}
	state, err := ResolveWorkflowObjectAuthorizationState(store, 2, 7, 4)
	require.NoError(t, err)
	assert.True(t, AuthorizeWorkflowReadForState(workflow, state).Allowed)
	workflow.AccessPolicy.ViewRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}
	assert.False(t, AuthorizeWorkflowReadForState(workflow, state).Allowed)
	store.grant = 0
	state, err = ResolveWorkflowObjectAuthorizationState(store, 2, 7, 4)
	require.NoError(t, err)
	workflow.AccessPolicy.ViewRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceGuest}
	assert.False(t, AuthorizeWorkflowReadForState(workflow, state).Allowed)
}
