package project_test

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowfactory "github.com/semaphoreui/semaphore/pro/db/factory"
	projectbackup "github.com/semaphoreui/semaphore/services/project"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupRestoreWorkflowIntegrationRoundTripUsesEnhancedStore(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "integration source"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "Repository", GitURL: "https://example.test/repository.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{Name: "Deploy", Playbook: "deploy.yml", ProjectID: project.ID, RepositoryID: repository.ID})
	require.NoError(t, err)
	workflows := workflowfactory.NewWorkflowStore(store)
	workflow, err := workflows.CreateWorkflowTemplate(db.WorkflowTemplate{ProjectID: project.ID, Name: "Inbound workflow"})
	require.NoError(t, err)
	principal, err := store.CreateUserWithoutPassword(db.User{Username: "bound-principal", Name: "Bound principal", Email: "bound-principal@example.test"})
	require.NoError(t, err)

	_, err = store.CreateIntegration(db.Integration{Name: "template integration", ProjectID: project.ID, TemplateID: &template.ID})
	require.NoError(t, err)
	workflowIntegration, err := store.CreateIntegration(db.Integration{
		Name: "workflow integration", ProjectID: project.ID, WorkflowID: &workflow.ID, WorkflowUserID: &principal.ID,
	})
	require.NoError(t, err)
	_, err = store.CreateIntegrationAlias(db.IntegrationAlias{Alias: "restored-workflow-alias", ProjectID: project.ID, IntegrationID: &workflowIntegration.ID})
	require.NoError(t, err)
	_, err = store.CreateIntegrationExtractValue(project.ID, db.IntegrationExtractValue{
		Name: "release", IntegrationID: workflowIntegration.ID, ValueSource: db.IntegrationExtractBodyValue,
		BodyDataType: db.IntegrationBodyDataJSON, Key: "release", Variable: "RELEASE", VariableType: "environment",
	})
	require.NoError(t, err)

	backup, err := projectbackup.GetBackup(project.ID, store, workflows)
	require.NoError(t, err)
	require.Len(t, backup.Workflows, 1)
	require.Len(t, backup.Integration, 2)
	encoded, err := backup.Marshal()
	require.NoError(t, err)
	imported := &projectbackup.BackupFormat{}
	require.NoError(t, imported.Unmarshal(encoded))
	imported.Meta.Name = "integration destination"

	destination := coresql.InitConfigCreateTestStore()
	t.Cleanup(destination.Close)
	destinationProject, err := destination.CreateProject(db.Project{Name: "existing destination project"})
	require.NoError(t, err)
	destinationWorkflows := workflowfactory.NewWorkflowStore(destination)
	_, err = destinationWorkflows.CreateWorkflowTemplate(db.WorkflowTemplate{ProjectID: destinationProject.ID, Name: "existing destination workflow"})
	require.NoError(t, err)
	restoreActor, err := destination.CreateUserWithoutPassword(db.User{Username: "restore-actor", Name: "Restore actor", Email: "restore-actor@example.test", Admin: true})
	require.NoError(t, err)
	restoredProject, err := imported.Restore(restoreActor, destination, destinationWorkflows)
	require.NoError(t, err)

	restoredIntegrations, err := destination.GetIntegrations(restoredProject.ID, db.RetrieveQueryParams{}, false)
	require.NoError(t, err)
	require.Len(t, restoredIntegrations, 2)
	byName := make(map[string]db.Integration, len(restoredIntegrations))
	for _, integration := range restoredIntegrations {
		byName[integration.Name] = integration
	}
	restoredWorkflowIntegration := byName["workflow integration"]
	require.NotNil(t, restoredWorkflowIntegration.WorkflowID)
	assert.NotEqual(t, workflow.ID, *restoredWorkflowIntegration.WorkflowID)
	assert.Nil(t, restoredWorkflowIntegration.WorkflowUserID)
	assert.Nil(t, restoredWorkflowIntegration.TemplateID)
	restoredWorkflow, err := destinationWorkflows.GetWorkflowTemplate(restoredProject.ID, *restoredWorkflowIntegration.WorkflowID)
	require.NoError(t, err)
	assert.Equal(t, "Inbound workflow", restoredWorkflow.Name)

	restoredTemplateIntegration := byName["template integration"]
	require.NotNil(t, restoredTemplateIntegration.TemplateID)
	assert.Nil(t, restoredTemplateIntegration.WorkflowID)
	aliases, err := destination.GetIntegrationAliases(restoredProject.ID, &restoredWorkflowIntegration.ID)
	require.NoError(t, err)
	require.Len(t, aliases, 1)
	assert.Equal(t, "restored-workflow-alias", aliases[0].Alias)
	extractors, err := destination.GetIntegrationExtractValues(restoredProject.ID, db.RetrieveQueryParams{}, restoredWorkflowIntegration.ID)
	require.NoError(t, err)
	require.Len(t, extractors, 1)
	assert.Equal(t, "RELEASE", extractors[0].Variable)
}
