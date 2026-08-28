package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	workflowSQL "github.com/semaphoreui/semaphore/pro/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowDefinitionServicePreventsInvalidPersistence(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Service test"})
	require.NoError(t, err)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)

	_, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "Invalid",
		Nodes: []db.WorkflowNode{{
			ID: -1, Kind: db.WorkflowNodeTaskKind, TemplateID: 987654,
		}},
	})

	require.NoError(t, err)
	assert.False(t, validation.Valid)
	assert.Contains(t, workflowServiceIssueCodes(validation), "WORKFLOW_TEMPLATE_NOT_IN_PROJECT")
	stored, err := repository.GetWorkflowTemplates(project.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	assert.Empty(t, stored)
}

func TestWorkflowDefinitionServiceNormalizesBeforeCreate(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	defer store.Close()
	project, err := store.CreateProject(db.Project{Name: "Service test"})
	require.NoError(t, err)
	templateID := insertWorkflowTestTemplate(t, store, project.ID)
	repository := workflowSQL.NewWorkflowStore(store.GetConnection())
	service := NewWorkflowDefinitionService(repository, store)

	created, validation, err := service.Create(project.ID, db.WorkflowTemplate{
		Name: "  Build  ",
		Nodes: []db.WorkflowNode{{
			ID: -1, TemplateID: templateID,
		}},
	})

	require.NoError(t, err)
	require.True(t, validation.Valid, validation.Issues)
	assert.Equal(t, "Build", created.Name)
	assert.Equal(t, db.WorkflowDefinitionVersion, created.DefinitionVersion)
	assert.Equal(t, db.WorkflowNodeTaskKind, created.Nodes[0].Kind)
	assert.Equal(t, db.WorkflowConvergenceAll, created.Nodes[0].ConvergenceMode)
}

func insertWorkflowTestTemplate(t *testing.T, store *coresql.SqlDb, projectID int) int {
	t.Helper()
	keyResult, err := store.GetConnection().Exec(
		"insert into access_key(name, type, project_id) values (?, ?, ?)",
		"Test key", "none", projectID,
	)
	require.NoError(t, err)
	keyID, err := keyResult.LastInsertId()
	require.NoError(t, err)
	repositoryResult, err := store.GetConnection().Exec(
		"insert into project__repository(project_id, git_url, ssh_key_id, name) values (?, ?, ?, ?)",
		projectID, "https://example.invalid/repository.git", keyID, "Repository",
	)
	require.NoError(t, err)
	repositoryID, err := repositoryResult.LastInsertId()
	require.NoError(t, err)
	result, err := store.GetConnection().Exec(
		"insert into project__template(project_id, repository_id, name, playbook, type, app) values (?, ?, ?, ?, ?, ?)",
		projectID, repositoryID, "Build", "site.yml", "", "ansible",
	)
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	return int(id)
}

func workflowServiceIssueCodes(result db.WorkflowValidationResult) []string {
	codes := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		codes = append(codes, issue.Code)
	}
	return codes
}
