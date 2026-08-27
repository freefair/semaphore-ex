package sql

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type summaryFixture struct {
	store      *coresql.SqlDb
	repository db.AnsibleTaskRepository
	projectID  int
	task       db.Task
}

func newSummaryFixture(t *testing.T, name string) summaryFixture {
	t.Helper()
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: name})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, Name: "summary repo", GitURL: "https://example.com/repo.git",
		GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{
		ProjectID: project.ID, Name: "summary inventory", Type: db.InventoryStatic,
		Inventory: "localhost,", SSHKeyID: &key.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, Name: "summary task",
		Playbook: "site.yml", App: db.AppAnsible, InventoryID: &inventory.ID,
	})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{
		TemplateID: template.ID, ProjectID: project.ID, Status: task_logger.TaskRunningStatus,
		Playbook: "site.yml", Created: time.Now().UTC(),
	}, 0)
	require.NoError(t, err)
	return summaryFixture{
		store: store, repository: NewAnsibleTask(store.GetConnection()), projectID: project.ID, task: task,
	}
}

func TestTaskSummaryRepository_PersistsIdempotentOutOfOrderResults(t *testing.T) {
	fixture := newSummaryFixture(t, "summary persistence")
	now := time.Now().UTC()

	complete := db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventComplete,
		EventID: "run:complete", ExpectedHosts: 1,
	}
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, complete, now))
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventResult,
		EventID: "task:web:failed", StageID: "task", Stage: "Deploy", Host: "web-01",
		Status: "failed", Error: "redacted failure", DurationMS: 12,
	}, now.Add(time.Second)))
	host := db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventHost,
		EventID: "host:web-01", Host: "web-01", Status: "failed", Failed: 1,
	}
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, host, now.Add(2*time.Second)))
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, host, now.Add(2*time.Second)))

	// A claimed terminal status cannot make the summary complete before the
	// task row itself is terminal.
	require.NoError(t, fixture.repository.FinalizeTaskSummary(
		fixture.projectID, fixture.task.ID, task_logger.TaskSuccessStatus, &now, nil))
	summary, err := fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskSummaryCollecting, summary.State)

	fixture.task.Status = task_logger.TaskSuccessStatus
	ended := now.Add(3 * time.Second)
	fixture.task.End = &ended
	require.NoError(t, fixture.store.UpdateTask(fixture.task))
	require.NoError(t, fixture.repository.FinalizeTaskSummary(
		fixture.projectID, fixture.task.ID, fixture.task.Status, &now, &ended))

	summary, err = fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskSummaryComplete, summary.State)
	assert.Equal(t, 3, summary.EventCount, "duplicate event must not be counted")
	assert.Equal(t, 1, summary.TotalHosts)
	assert.Equal(t, 1, summary.FailedHosts)
	assert.Equal(t, string(task_logger.TaskSuccessStatus), summary.TaskStatus)

	errorsPage, err := fixture.repository.GetTaskSummaryErrors(fixture.projectID, fixture.task.ID, db.RetrieveQueryParams{})
	require.NoError(t, err)
	require.Len(t, errorsPage.Items, 1)
	assert.Equal(t, "redacted failure", errorsPage.Items[0].Error)
}

func TestTaskSummaryRepository_RepairsPartialSummaryWithoutRawLogs(t *testing.T) {
	fixture := newSummaryFixture(t, "summary repair")
	now := time.Now().UTC()
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventComplete,
		EventID: "run:complete", ExpectedHosts: 2,
	}, now))
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventHost,
		EventID: "host:web-01", Host: "web-01", Status: "success", Ok: 1,
	}, now))
	fixture.task.Status = task_logger.TaskFailStatus
	require.NoError(t, fixture.store.UpdateTask(fixture.task))
	require.NoError(t, fixture.repository.FinalizeTaskSummary(fixture.projectID, fixture.task.ID, fixture.task.Status, nil, nil))
	summary, err := fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskSummaryPartial, summary.State)
	assert.Contains(t, summary.Diagnostic, "1 of 2")

	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventHost,
		EventID: "host:web-02", Host: "web-02", Status: "failed", Unreachable: 1,
	}, now))
	require.NoError(t, fixture.repository.RepairTaskSummary(fixture.projectID, fixture.task.ID, fixture.task.Status))
	summary, err = fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskSummaryComplete, summary.State)
	assert.Empty(t, summary.Diagnostic)
}

func TestTaskSummaryRepository_PersistsCollectionFailureAsPartial(t *testing.T) {
	fixture := newSummaryFixture(t, "summary collection failure")
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(
		fixture.projectID,
		fixture.task.ID,
		db.TaskSummaryEvent{
			Version: db.TaskSummarySchemaVersion,
			Kind:    db.TaskSummaryEventFailure,
			EventID: "collection:setup",
			Error:   "callback setup failed",
		},
		time.Now().UTC(),
	))
	fixture.task.Status = task_logger.TaskFailStatus
	require.NoError(t, fixture.store.UpdateTask(fixture.task))
	require.NoError(t, fixture.repository.FinalizeTaskSummary(
		fixture.projectID, fixture.task.ID, fixture.task.Status, nil, nil,
	))

	summary, err := fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskSummaryPartial, summary.State)
	assert.Contains(t, summary.Diagnostic, "callback setup failed")
	assert.Equal(t, string(task_logger.TaskFailStatus), summary.TaskStatus)
}

func TestTaskSummaryRepository_BoundsPagesAndScopesEveryRead(t *testing.T) {
	fixture := newSummaryFixture(t, "summary pagination")
	now := time.Now().UTC()
	for index := 0; index < 5; index++ {
		require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{
			Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventHost,
			EventID: fmt.Sprintf("host:%d", index), Host: fmt.Sprintf("web-%02d", index), Status: "success",
		}, now))
	}
	first, err := fixture.repository.GetTaskSummaryHosts(
		fixture.projectID, fixture.task.ID, db.RetrieveQueryParams{Count: 2})
	require.NoError(t, err)
	require.Len(t, first.Items, 2)
	require.NotNil(t, first.NextCursor)
	second, err := fixture.repository.GetTaskSummaryHosts(
		fixture.projectID, fixture.task.ID, db.RetrieveQueryParams{Count: 2, BeforeID: *first.NextCursor})
	require.NoError(t, err)
	require.Len(t, second.Items, 2)
	assert.NotEqual(t, first.Items[0].ID, second.Items[0].ID)

	other, err := fixture.store.CreateProject(db.Project{Name: "other project"})
	require.NoError(t, err)
	_, err = fixture.repository.GetTaskSummary(other.ID, fixture.task.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = fixture.repository.GetTaskSummaryErrors(other.ID, fixture.task.ID, db.RetrieveQueryParams{})
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestTaskSummaryRepository_UnsupportedVersionAndTaskDeletion(t *testing.T) {
	fixture := newSummaryFixture(t, "summary unsupported")
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{
		Version: 99, Kind: db.TaskSummaryEventComplete, EventID: "future",
	}, time.Now().UTC()))
	fixture.task.Status = task_logger.TaskSuccessStatus
	require.NoError(t, fixture.store.UpdateTask(fixture.task))
	require.NoError(t, fixture.repository.FinalizeTaskSummary(fixture.projectID, fixture.task.ID, fixture.task.Status, nil, nil))
	summary, err := fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskSummaryUnsupported, summary.State)
	assert.Equal(t, 99, summary.RunnerResultVersion)

	require.NoError(t, fixture.store.DeleteTaskWithOutputs(fixture.projectID, fixture.task.ID))
	_, err = fixture.repository.GetTaskSummary(fixture.projectID, fixture.task.ID)
	assert.True(t, errors.Is(err, db.ErrNotFound))
}
