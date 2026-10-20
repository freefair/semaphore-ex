package projects

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	coreprojects "github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowfactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowIntegrationInputMutationRequiresEditorAndBoundPrincipalStartAccess(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "workflow integration mutation"})
	require.NoError(t, err)
	workflowStore := workflowfactory.NewWorkflowStore(store)
	workflow, err := workflowStore.CreateWorkflowTemplate(db.WorkflowTemplate{ProjectID: project.ID, Name: "restricted workflow"})
	require.NoError(t, err)

	resourceManager, err := store.CreateProjectRole(db.Role{
		ID: "resource_manager", Slug: "resource_manager", Name: "Resource manager", ProjectID: &project.ID,
		Permissions: db.CanManageProjectResources, Revision: 1,
	})
	require.NoError(t, err)
	workflowManager, err := store.CreateProjectRole(db.Role{
		ID: "workflow_manager", Slug: "workflow_manager", Name: "Workflow manager", ProjectID: &project.ID,
		Permissions: db.CanManageProjectResources | db.CanViewWorkflows | db.CanStartWorkflows, Revision: 1,
	})
	require.NoError(t, err)
	boundPrincipal, err := store.CreateUserWithoutPassword(db.User{Username: "bound-workflow-principal", Name: "Bound workflow principal", Email: "bound-workflow-principal@example.test"})
	require.NoError(t, err)
	resourceOnlyEditor, err := store.CreateUserWithoutPassword(db.User{Username: "resource-only-editor", Name: "Resource only editor", Email: "resource-only-editor@example.test"})
	require.NoError(t, err)
	workflowEditor, err := store.CreateUserWithoutPassword(db.User{Username: "workflow-editor", Name: "Workflow editor", Email: "workflow-editor@example.test"})
	require.NoError(t, err)
	for _, membership := range []db.ProjectUser{
		{ProjectID: project.ID, UserID: boundPrincipal.ID, RoleID: &workflowManager.ID, Revision: 1},
		{ProjectID: project.ID, UserID: resourceOnlyEditor.ID, RoleID: &resourceManager.ID, Revision: 1},
		{ProjectID: project.ID, UserID: workflowEditor.ID, RoleID: &workflowManager.ID, Revision: 1},
	} {
		_, err = store.CreateProjectUser(membership)
		require.NoError(t, err)
	}
	integration, err := store.CreateIntegration(db.Integration{
		Name: "inbound workflow", ProjectID: project.ID, WorkflowID: &workflow.ID, WorkflowUserID: &boundPrincipal.ID,
	})
	require.NoError(t, err)
	controller := coreprojects.NewIntegrationController(store, workflowStore, features.NewCapabilityProvider(store))
	called := false
	handler := controller.WorkflowIntegrationInputMutationMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	request := func(user *db.User) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/project/1/integrations/1/values", nil)
		r = helpers.SetContextValue(r, "integration", integration)
		return helpers.SetContextValue(r, "user", user)
	}

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, request(&resourceOnlyEditor))
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.False(t, called, "resource management alone must not alter a bound workflow webhook")

	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, request(&workflowEditor))
	assert.Equal(t, http.StatusOK, allowed.Code)
	assert.True(t, called)

	called = false
	require.NoError(t, store.DeleteProjectUser(project.ID, boundPrincipal.ID))
	revokedPrincipal := httptest.NewRecorder()
	handler.ServeHTTP(revokedPrincipal, request(&workflowEditor))
	assert.Equal(t, http.StatusForbidden, revokedPrincipal.Code)
	assert.False(t, called, "child mutations must fail when the stored run principal no longer has start access")

	require.NoError(t, store.DeleteUser(boundPrincipal.ID))
	deletedPrincipal := httptest.NewRecorder()
	handler.ServeHTTP(deletedPrincipal, request(&workflowEditor))
	assert.Equal(t, http.StatusForbidden, deletedPrincipal.Code)
	assert.False(t, called, "a missing stored principal must fail closed without reaching the child mutation")
}

func TestWorkflowIntegrationAliasRoutesRequireWorkflowStartAuthority(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "mixed aliases"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "mixed aliases", GitURL: "https://example.test/mixed.git", GitBranch: "main"})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repository.ID, Name: "template target", Playbook: "deploy.yml"})
	require.NoError(t, err)
	workflowStore := workflowfactory.NewWorkflowStore(store)
	workflow, err := workflowStore.CreateWorkflowTemplate(db.WorkflowTemplate{ProjectID: project.ID, Name: "workflow target"})
	require.NoError(t, err)
	resourceRole, err := store.CreateProjectRole(db.Role{ID: "mixed_resource", Slug: "mixed_resource", Name: "Mixed resource", ProjectID: &project.ID, Permissions: db.CanManageProjectResources, Revision: 1})
	require.NoError(t, err)
	workflowRole, err := store.CreateProjectRole(db.Role{ID: "mixed_workflow", Slug: "mixed_workflow", Name: "Mixed workflow", ProjectID: &project.ID, Permissions: db.CanManageProjectResources | db.CanViewWorkflows | db.CanStartWorkflows, Revision: 1})
	require.NoError(t, err)
	principal, err := store.CreateUserWithoutPassword(db.User{Username: "mixed-principal", Name: "Mixed principal", Email: "mixed-principal@example.test"})
	require.NoError(t, err)
	resourceEditor, err := store.CreateUserWithoutPassword(db.User{Username: "mixed-resource-editor", Name: "Mixed resource editor", Email: "mixed-resource-editor@example.test"})
	require.NoError(t, err)
	workflowEditor, err := store.CreateUserWithoutPassword(db.User{Username: "mixed-workflow-editor", Name: "Mixed workflow editor", Email: "mixed-workflow-editor@example.test"})
	require.NoError(t, err)
	for _, membership := range []db.ProjectUser{
		{ProjectID: project.ID, UserID: principal.ID, RoleID: &workflowRole.ID, Revision: 1},
		{ProjectID: project.ID, UserID: resourceEditor.ID, RoleID: &resourceRole.ID, Revision: 1},
		{ProjectID: project.ID, UserID: workflowEditor.ID, RoleID: &workflowRole.ID, Revision: 1},
	} {
		_, err = store.CreateProjectUser(membership)
		require.NoError(t, err)
	}
	workflowIntegration, err := store.CreateIntegration(db.Integration{Name: "workflow webhook", ProjectID: project.ID, WorkflowID: &workflow.ID, WorkflowUserID: &principal.ID, Searchable: true})
	require.NoError(t, err)
	_, err = store.CreateIntegration(db.Integration{Name: "template webhook", ProjectID: project.ID, TemplateID: &template.ID, Searchable: true})
	require.NoError(t, err)
	controller := coreprojects.NewIntegrationController(store, workflowStore, features.NewCapabilityProvider(store))
	request := func(method string, user *db.User, integration *db.Integration) *http.Request {
		path := "/api/project/" + strconv.Itoa(project.ID) + "/integrations"
		if integration != nil {
			path += "/" + strconv.Itoa(integration.ID)
		}
		path += "/aliases"
		r := httptest.NewRequest(method, path, nil)
		r = helpers.SetContextValue(r, "store", store)
		r = helpers.SetContextValue(r, "project", project)
		r = helpers.SetContextValue(r, "user", user)
		if integration != nil {
			r = helpers.SetContextValue(r, "integration", *integration)
		}
		return mux.SetURLVars(r, map[string]string{"project_id": "1", "integration_id": "1"})
	}

	perIntegration := mux.NewRouter()
	perIntegration.Handle("/api/project/{project_id}/integrations/{integration_id}/aliases", controller.WorkflowIntegrationAliasMiddleware(http.HandlerFunc(coreprojects.GetIntegrationAlias))).Methods(http.MethodGet)
	deniedSingle := httptest.NewRecorder()
	perIntegration.ServeHTTP(deniedSingle, request(http.MethodGet, &resourceEditor, &workflowIntegration))
	assert.Equal(t, http.StatusForbidden, deniedSingle.Code)
	allowedSingle := httptest.NewRecorder()
	perIntegration.ServeHTTP(allowedSingle, request(http.MethodGet, &workflowEditor, &workflowIntegration))
	assert.Equal(t, http.StatusOK, allowedSingle.Code)

	projectAliases := mux.NewRouter()
	projectAliases.Handle("/api/project/{project_id}/integrations/aliases", controller.ProjectWorkflowIntegrationAliasMiddleware(http.HandlerFunc(coreprojects.GetIntegrationAlias))).Methods(http.MethodGet)
	projectAliases.Handle("/api/project/{project_id}/integrations/aliases", controller.ProjectWorkflowIntegrationAliasMiddleware(http.HandlerFunc(coreprojects.AddIntegrationAlias))).Methods(http.MethodPost)
	deniedProjectRead := httptest.NewRecorder()
	projectAliases.ServeHTTP(deniedProjectRead, request(http.MethodGet, &resourceEditor, nil))
	assert.Equal(t, http.StatusForbidden, deniedProjectRead.Code)
	deniedProjectCreate := httptest.NewRecorder()
	projectAliases.ServeHTTP(deniedProjectCreate, request(http.MethodPost, &resourceEditor, nil))
	assert.Equal(t, http.StatusForbidden, deniedProjectCreate.Code)
	projectAliasesBefore, err := store.GetIntegrationAliases(project.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, projectAliasesBefore, "a denied editor cannot mint a bearer URL for a workflow fan-out")

	createdProjectAlias := httptest.NewRecorder()
	projectAliases.ServeHTTP(createdProjectAlias, request(http.MethodPost, &workflowEditor, nil))
	assert.Equal(t, http.StatusOK, createdProjectAlias.Code)
	aliases, err := store.GetIntegrationAliases(project.ID, nil)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	targets, level, err := store.GetIntegrationsByAlias(aliases[0].Alias)
	require.NoError(t, err)
	assert.EqualValues(t, db.IntegrationAliasProject, level)
	require.Len(t, targets, 2, "the protected project alias would deliver to both template and workflow targets")

	require.NoError(t, store.DeleteProjectUser(project.ID, principal.ID))
	revokedProjectRead := httptest.NewRecorder()
	projectAliases.ServeHTTP(revokedProjectRead, request(http.MethodGet, &workflowEditor, nil))
	assert.Equal(t, http.StatusForbidden, revokedProjectRead.Code)
	require.NoError(t, store.DeleteUser(principal.ID))
	deletedProjectRead := httptest.NewRecorder()
	projectAliases.ServeHTTP(deletedProjectRead, request(http.MethodGet, &workflowEditor, nil))
	assert.Equal(t, http.StatusForbidden, deletedProjectRead.Code)

	templateOnlyProject, err := store.CreateProject(db.Project{Name: "template aliases"})
	require.NoError(t, err)
	templateOnlyKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &templateOnlyProject.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	templateOnlyRepository, err := store.CreateRepository(db.Repository{ProjectID: templateOnlyProject.ID, SSHKeyID: templateOnlyKey.ID, Name: "template aliases", GitURL: "https://example.test/template.git", GitBranch: "main"})
	require.NoError(t, err)
	templateOnly, err := store.CreateTemplate(db.Template{ProjectID: templateOnlyProject.ID, RepositoryID: templateOnlyRepository.ID, Name: "template only", Playbook: "deploy.yml"})
	require.NoError(t, err)
	_, err = store.CreateIntegration(db.Integration{Name: "template only webhook", ProjectID: templateOnlyProject.ID, TemplateID: &templateOnly.ID, Searchable: true})
	require.NoError(t, err)
	templateOnlyRole, err := store.CreateProjectRole(db.Role{ID: "template_alias_resource", Slug: "template_alias_resource", Name: "Template alias resource", ProjectID: &templateOnlyProject.ID, Permissions: db.CanManageProjectResources, Revision: 1})
	require.NoError(t, err)
	_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: templateOnlyProject.ID, UserID: resourceEditor.ID, RoleID: &templateOnlyRole.ID, Revision: 1})
	require.NoError(t, err)
	templateOnlyRequest := request(http.MethodPost, &resourceEditor, nil)
	templateOnlyRequest = helpers.SetContextValue(templateOnlyRequest, "project", templateOnlyProject)
	templateOnlyCreate := httptest.NewRecorder()
	projectAliases.ServeHTTP(templateOnlyCreate, templateOnlyRequest)
	assert.Equal(t, http.StatusOK, templateOnlyCreate.Code, "template-only project aliases retain the existing resource-management contract")
}
