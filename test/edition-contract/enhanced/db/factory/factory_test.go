package factory

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowStoreReturnsTasksForRunDashboard(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "Workflow task links"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "repo",
		GitURL: "https://example.invalid/repo.git", GitBranch: "main",
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "First", Playbook: "first.sh",
	})
	require.NoError(t, err)
	runID, nodeID := 41, 51
	created, err := store.CreateTask(db.Task{
		ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskWaitingStatus,
		WorkflowRunID: &runID, WorkflowNodeID: &nodeID,
	}, 0)
	require.NoError(t, err)

	tasks, err := NewWorkflowStore(store).GetWorkflowRunTasks(project.ID, runID, db.RetrieveQueryParams{})

	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, created.ID, tasks[0].ID)
	require.NotNil(t, tasks[0].WorkflowNodeID)
	assert.Equal(t, nodeID, *tasks[0].WorkflowNodeID)
}

func TestPolicyGuardrailStoreIsAvailableForSQLStore(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)

	repository := NewPolicyGuardrailStore(store)

	require.NotNil(t, repository)
	_, admissionOK := repository.(pro_interfaces.PolicyGuardrailAdmissionRepository)
	assert.True(t, admissionOK)
}

func TestPolicyGuardrailStoreIsUnavailableWithoutSQLConnection(t *testing.T) {
	assert.Nil(t, NewPolicyGuardrailStore(nil))
}
