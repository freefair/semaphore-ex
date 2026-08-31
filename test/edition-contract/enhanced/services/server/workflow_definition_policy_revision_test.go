package server

import (
	"encoding/json"
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
	versions, err := service.ListVersions(project.ID, created.ID, db.RetrieveQueryParams{}, actor)
	require.NoError(t, err)
	assert.Len(t, versions, 2, "the losing CAS update must not append a version")
}

func TestWorkflowDefinitionUpdateDoesNotMutateCallerGraph(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "immutable update input"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	actor := &db.User{ID: 1, Admin: true}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "immutable", Nodes: []db.WorkflowNode{{ID: -1, TemplateID: templateID}},
	}, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid)

	candidate := created
	candidate.Nodes = append(candidate.Nodes, db.WorkflowNode{ID: -2, TemplateID: templateID})
	candidate.Edges = append(candidate.Edges, db.WorkflowEdge{
		ID: -1, SourceNodeID: created.Nodes[0].ID, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess,
	})
	expected := candidate
	expected.Nodes = append([]db.WorkflowNode(nil), candidate.Nodes...)
	expected.Edges = append([]db.WorkflowEdge(nil), candidate.Edges...)

	_, validation, err = service.Update(project.ID, created.ID, candidate, actor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, expected, candidate, "workflow updates must not mutate the caller-owned graph")
}

func TestWorkflowDefinitionClonePreservesEmptyPolicyRoleSlices(t *testing.T) {
	input := db.WorkflowTemplate{
		AccessPolicy: db.WorkflowAccessPolicy{
			ViewRoleIDs:  []db.ProjectRoleReference{},
			StartRoleIDs: []db.ProjectRoleReference{},
		},
		Nodes: []db.WorkflowNode{{
			ApprovalRolePolicy: db.WorkflowApprovalRolePolicy{RoleIDs: []db.ProjectRoleReference{}},
		}},
	}

	cloned, err := cloneWorkflowDefinitionInput(input)
	require.NoError(t, err)
	assert.NotNil(t, cloned.AccessPolicy.ViewRoleIDs)
	assert.NotNil(t, cloned.AccessPolicy.StartRoleIDs)
	assert.NotNil(t, cloned.Nodes[0].ApprovalRolePolicy.RoleIDs)
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

func TestWorkflowDefinitionUsesNestedPolicyRevisionsAsWireCASInput(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "nested policy CAS"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	service := NewWorkflowDefinitionService(workflowSQL.NewWorkflowStore(store.GetConnection()), store)
	admin := &db.User{ID: 1, Admin: true}
	editor := createWorkflowDefinitionActor(t, store, project.ID, "nested-policy-editor", db.CanViewWorkflows|db.CanEditWorkflows)
	approvalPolicy := db.WorkflowApprovalRolePolicy{
		Mode:                     db.WorkflowApprovalRoleModeAnyOf,
		RoleIDs:                  []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner},
		MinimumDistinctApprovers: 1,
	}
	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "nested policy", Nodes: []db.WorkflowNode{
			{ID: -1, TemplateID: templateID},
			{ID: -2, Kind: db.WorkflowNodeApprovalKind, ApprovalRolePolicy: approvalPolicy},
		}, Edges: []db.WorkflowEdge{{ID: -1, SourceNodeID: -1, DestinationNodeID: -2, Condition: db.WorkflowEdgeOnSuccess}},
	}, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	require.Equal(t, 1, created.AccessPolicyRevision)
	require.Equal(t, 1, created.AccessPolicy.Revision)
	require.Equal(t, 1, created.Nodes[1].ApprovalRolePolicyRevision)
	require.Equal(t, 1, created.Nodes[1].ApprovalRolePolicy.Revision)

	browser := workflowDefinitionWireRoundTrip(t, created)
	assert.Zero(t, browser.AccessPolicyRevision, "the storage-only policy revision is never a client field")
	assert.Zero(t, browser.Nodes[1].ApprovalRolePolicyRevision, "the storage-only approval revision is never a client field")
	require.Equal(t, 1, browser.AccessPolicy.Revision)
	require.Equal(t, 1, browser.Nodes[1].ApprovalRolePolicy.Revision)
	browser.Name = "ordinary browser save"
	ordinary, validation, err := service.Update(project.ID, created.ID, browser, &editor)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 1, ordinary.AccessPolicyRevision)
	assert.Equal(t, 1, ordinary.Nodes[1].ApprovalRolePolicyRevision)

	policyChange := workflowDefinitionWireRoundTrip(t, ordinary)
	policyChange.AccessPolicy.ViewRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}
	_, _, err = service.Update(project.ID, created.ID, policyChange, &editor)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowPermissionDenied, "only workflow administrators may change access policy")

	policyUpdated, validation, err := service.Update(project.ID, created.ID, policyChange, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 2, policyUpdated.AccessPolicyRevision)
	assert.Equal(t, 2, policyUpdated.AccessPolicy.Revision, "an accepted policy change increments exactly once")

	staleAccessPolicy := workflowDefinitionWireRoundTrip(t, ordinary)
	staleAccessPolicy.Revision = policyUpdated.Revision
	staleAccessPolicy.AccessPolicy.StartRoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceOwner}
	_, _, err = service.Update(project.ID, created.ID, staleAccessPolicy, admin)
	assert.ErrorIs(t, err, pro_interfaces.ErrWorkflowRevisionConflict, "stale nested access policy revisions fail closed")

	approvalUpdatedRequest := workflowDefinitionWireRoundTrip(t, policyUpdated)
	approvalUpdatedRequest.Nodes[1].ApprovalRolePolicy.RoleIDs = []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}
	approvalUpdated, validation, err := service.Update(project.ID, created.ID, approvalUpdatedRequest, admin)
	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, 2, approvalUpdated.Nodes[1].ApprovalRolePolicyRevision)
	assert.Equal(t, 2, approvalUpdated.Nodes[1].ApprovalRolePolicy.Revision, "an accepted approval policy change increments exactly once")
}

func workflowDefinitionWireRoundTrip(t *testing.T, workflow db.WorkflowTemplate) db.WorkflowTemplate {
	t.Helper()
	payload, err := json.Marshal(workflow)
	require.NoError(t, err)
	var browser db.WorkflowTemplate
	require.NoError(t, json.Unmarshal(payload, &browser))
	return browser
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
