package server

import (
	"sync"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDefinitionConcurrentSameRevisionHasOneWinner(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "concurrent policy"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	actor := &db.User{ID: 1, Admin: true}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{Name: "concurrent", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}}}, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid)
	left, right := created, created
	left.Name, right.Name = "left", "right"
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, candidate := range []db.WorkflowTemplate{left, right} {
		wg.Add(1)
		go func(value db.WorkflowTemplate) {
			defer wg.Done()
			_, _, updateErr := service.Update(project.ID, created.ID, value, actor)
			results <- updateErr
		}(candidate)
	}
	wg.Wait()
	close(results)
	var success, conflict int
	for updateErr := range results {
		if updateErr == nil {
			success++
		} else if updateErr == pro_interfaces.ErrWorkflowRevisionConflict {
			conflict++
		}
	}
	assert.Equal(t, 1, success)
	assert.Equal(t, 1, conflict)
}

func TestWorkflowDefinitionPolicyRevisionsAreServerOwned(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "policy revisions"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	actor := &db.User{ID: 1, Admin: true}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{Name: "policy", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}}}, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 1, created.AccessPolicyRevision)
	assert.Equal(t, 1, created.AccessPolicy.Revision)

	unchanged := created
	unchanged.Name = "ordinary graph edit"
	updated, validation, err := service.Update(project.ID, created.ID, unchanged, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 1, updated.AccessPolicyRevision)

	changed := updated
	changed.AccessPolicy.ViewRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}
	updated, validation, err = service.Update(project.ID, created.ID, changed, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 2, updated.AccessPolicyRevision)
	assert.Equal(t, 2, updated.AccessPolicy.Revision)

	stale := updated
	stale.AccessPolicy.StartRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}
	stale.AccessPolicyRevision = 1
	stale.AccessPolicy.Revision = 1
	_, _, err = service.Update(project.ID, created.ID, stale, actor)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowRevisionConflict)
}

func TestWorkflowDefinitionApprovalPolicyRevisionUsesNodeIdentity(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "approval policy revisions"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	actor := &db.User{ID: 1, Admin: true}
	policy := db.WorkflowApprovalRolePolicy{Mode: db.WorkflowApprovalRoleModeAnyOf, RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}, MinimumDistinctApprovers: 1}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{Name: "approval", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}, {ID: -2, Kind: db.WorkflowNodeApprovalKind, ApprovalRolePolicy: policy}}, Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}}}, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	require.Equal(t, 1, created.Nodes[1].ApprovalRolePolicyRevision)

	changed := created
	changed.Nodes[1].ApprovalRolePolicy.RoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}
	updated, validation, err := service.Update(project.ID, created.ID, changed, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 2, updated.Nodes[1].ApprovalRolePolicyRevision)

	stale := updated
	stale.Nodes[1].ApprovalRolePolicy.RoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}
	stale.Nodes[1].ApprovalRolePolicyRevision = 1
	stale.Nodes[1].ApprovalRolePolicy.Revision = 1
	_, _, err = service.Update(project.ID, created.ID, stale, actor)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowRevisionConflict)
}

func TestWorkflowDefinitionEditRoleMayChangeOrdinaryTaskGraph(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "edit graph permission"})
	require.NoError(t, err)
	actor := createWorkflowDefinitionActor(t, store, project.ID, "editor", db.CanViewWorkflows|db.CanEditWorkflows)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	admin := &db.User{ID: 1, Admin: true}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name:  "editable graph",
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID, DisplayName: "before"}},
	}, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)

	requested := created
	requested.Nodes[0].DisplayName = "after"
	updated, validation, err := service.Update(project.ID, created.ID, requested, &actor)
	require.NoError(t, err)
	assert.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, "after", updated.Nodes[0].DisplayName)
	assert.Equal(t, 1, updated.AccessPolicyRevision)
}

func TestWorkflowDefinitionVisibleActorWithoutEditOrAdminCannotMutate(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "view-only mutation denial"})
	require.NoError(t, err)
	actor := createWorkflowDefinitionActor(t, store, project.ID, "viewer", db.CanViewWorkflows)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	admin := &db.User{ID: 1, Admin: true}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "viewable", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}},
	}, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)

	requested := created
	requested.Name = "forbidden"
	_, _, err = service.Update(project.ID, created.ID, requested, &actor)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied)
}

func TestWorkflowDefinitionGetHidesWorkflowDeniedByViewPolicy(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "view policy concealment"})
	require.NoError(t, err)
	actor := createWorkflowDefinitionActor(t, store, project.ID, "viewer", db.CanViewWorkflows|db.CanEditWorkflows)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	admin := &db.User{ID: 1, Admin: true}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name:  "owners only",
		Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}},
		AccessPolicy: db.WorkflowAccessPolicy{
			ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		},
	}, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)

	_, err = service.Get(project.ID, created.ID, &actor)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestWorkflowDefinitionApprovalPoliciesCannotBeDowngraded(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "approval policy downgrade"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)
	admin := &db.User{ID: 1, Admin: true}

	_, _, err = service.Create(project.ID, db.WorkflowTemplate{Name: "empty approval", Nodes: []db.WorkflowNode{{ID: -1, Kind: db.WorkflowNodeApprovalKind}}}, admin)
	require.ErrorContains(t, err, "require a role policy")

	policy := db.WorkflowApprovalRolePolicy{Mode: db.WorkflowApprovalRoleModeAnyOf, RoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}, MinimumDistinctApprovers: 1}
	explicit, validation, err := service.Create(project.ID, db.WorkflowTemplate{Name: "explicit approval", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}, {ID: -2, Kind: db.WorkflowNodeApprovalKind, ApprovalRolePolicy: policy}}, Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}}}, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid)
	cleared := explicit
	cleared.Nodes[1].ApprovalRolePolicy = db.WorkflowApprovalRolePolicy{}
	_, _, err = service.Update(project.ID, explicit.ID, cleared, admin)
	require.ErrorContains(t, err, "cannot be cleared")

	legacy, err := repository.CreateWorkflowTemplate(db.WorkflowTemplate{Name: "legacy approval", ProjectID: project.ID, Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}, {ID: -2, Kind: db.WorkflowNodeApprovalKind}}, Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}}})
	require.NoError(t, err)
	legacy.Name = "legacy approval renamed"
	updated, validation, err := service.Update(project.ID, legacy.ID, legacy, admin)
	require.NoError(t, err)
	assert.True(t, validation.Valid)
	assert.Equal(t, "legacy approval renamed", updated.Name)
}

func createWorkflowDefinitionActor(
	t *testing.T,
	store *coresql.SqlDb,
	projectID int,
	suffix string,
	permissions db.ProjectUserPermission,
) db.User {
	t.Helper()
	user, err := store.CreateUserWithoutPassword(db.User{
		Username: "workflow-definition-" + suffix,
		Name:     "Workflow Definition " + suffix,
		Email:    "workflow-definition-" + suffix + "@example.test",
	})
	require.NoError(t, err)
	roleID := db.ProjectRoleID("role_workflow_definition_" + suffix)
	role, err := store.CreateProjectRole(db.Role{
		ID:          roleID,
		Slug:        string(roleID),
		Name:        "Workflow Definition " + suffix,
		ProjectID:   &projectID,
		Permissions: permissions,
		Revision:    1,
	})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{
		ProjectID: projectID,
		UserID:    user.ID,
		Role:      db.ProjectNone,
		RoleID:    &role.ID,
		Revision:  1,
	})
	require.NoError(t, err)
	return user
}
