package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	projectService "github.com/semaphoreui/semaphore/services/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectBackupRequiresVisibilityOfAllObjects(t *testing.T) {
	f := newTaskControlFixture(t)
	permissions := db.CanReadProject | db.CanViewProjectResources | db.CanViewWorkflows
	role, err := f.store.CreateProjectRole(db.Role{ID: "backup-visibility-role", Slug: "backup-visibility-role", Name: "Backup visibility", ProjectID: &f.project.ID, Permissions: permissions, Revision: 1})
	require.NoError(t, err)
	user, err := f.store.CreateUserWithoutPassword(db.User{Username: "backup-visibility", Name: "Backup visibility", Email: "backup-visibility@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	workflowStore := proFactory.NewWorkflowStore(f.store)
	controller := NewBackupController(workflowStore)
	request := httptest.NewRequest(http.MethodGet, "/backup", nil)
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "project", f.project)
	request = helpers.SetContextValue(request, "store", f.store)
	request = helpers.SetContextValue(request, "basePermissions", permissions)
	assert.True(t, controller.canExportProject(request, f.project.ID))
	snapshot, err := projectService.GetBackup(f.project.ID, f.store, workflowStore)
	require.NoError(t, err)
	require.True(t, controller.canExportSnapshot(request, f.project.ID, snapshot))
	grant, err := f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.project.ID, TemplateID: f.template.ID, RoleID: &role.ID, DeniedPermissions: db.CanReadTemplate})
	require.NoError(t, err)
	assert.False(t, controller.canExportSnapshot(request, f.project.ID, snapshot), "a new deny invalidates the included snapshot")
	response := httptest.NewRecorder()
	controller.GetBackup(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, response.Body.String())
	require.NoError(t, f.store.DeleteTemplateRole(f.project.ID, f.template.ID, grant.ID, grant.Revision))
	workflow, err := workflowStore.CreateWorkflowTemplate(db.WorkflowTemplate{ProjectID: f.project.ID, Name: "restricted", DefinitionVersion: db.WorkflowDefinitionVersion, Nodes: []db.WorkflowNode{{ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: f.template.ID, ConvergenceMode: db.WorkflowConvergenceAll}}, AccessPolicy: db.WorkflowAccessPolicy{Revision: 1, ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}}})
	require.NoError(t, err)
	require.Positive(t, workflow.ID)
	response = httptest.NewRecorder()
	controller.GetBackup(response, request)
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, response.Body.String())
	adminRequest := helpers.SetContextValue(request, "user", &db.User{Admin: true})
	assert.True(t, controller.canExportProject(adminRequest, f.project.ID))
}

func TestBackupSnapshotRejectsObjectDeletedAfterGeneration(t *testing.T) {
	f := newTaskControlFixture(t)
	permissions := db.CanReadProject | db.CanViewProjectResources | db.CanViewWorkflows
	role, err := f.store.CreateProjectRole(db.Role{ID: "backup-deleted-role", Slug: "backup-deleted-role", Name: "Backup deleted", ProjectID: &f.project.ID, Permissions: permissions, Revision: 1})
	require.NoError(t, err)
	user, err := f.store.CreateUserWithoutPassword(db.User{Username: "backup-deleted", Name: "Backup deleted", Email: "backup-deleted@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.project.ID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
	require.NoError(t, err)
	workflowStore := proFactory.NewWorkflowStore(f.store)
	controller := NewBackupController(workflowStore)
	request := httptest.NewRequest(http.MethodGet, "/backup", nil)
	request = helpers.SetContextValue(request, "user", &user)
	request = helpers.SetContextValue(request, "project", f.project)
	request = helpers.SetContextValue(request, "store", f.store)
	snapshot, err := projectService.GetBackup(f.project.ID, f.store, workflowStore)
	require.NoError(t, err)
	require.True(t, controller.canExportSnapshot(request, f.project.ID, snapshot))
	require.NoError(t, f.store.DeleteTemplate(f.project.ID, f.template.ID))
	assert.False(t, controller.canExportSnapshot(request, f.project.ID, snapshot))
	assert.True(t, controller.canExportProject(request, f.project.ID), "a fresh list alone misses the deleted object")
}
