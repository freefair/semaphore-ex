package sql

import (
	"testing"
	"time"

	coredb "github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskControlStoreFencesExpiredOwnerAndRejectsStaleRelease(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	execution := pro_interfaces.TaskExecutionIdentity{RunnerID: 7, Generation: 2, StableID: "runner-job-17-2"}

	first, claimed, err := store.ClaimTaskControl(17, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.EqualValues(t, 1, first.FencingToken)

	blocked, claimed, err := store.ClaimTaskControl(17, execution, "boot-b", time.Minute)
	require.NoError(t, err)
	assert.False(t, claimed)
	assert.Equal(t, first.OwnerBootID, blocked.OwnerBootID)

	_, err = database.GetConnection().Exec("update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17)
	require.NoError(t, err)
	second, claimed, err := store.ClaimTaskControl(17, execution, "boot-b", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.EqualValues(t, 2, second.FencingToken)

	current, err := store.IsCurrentTaskControlLease(first)
	require.NoError(t, err)
	assert.False(t, current)
	released, err := store.ReleaseTaskControlLease(first)
	require.NoError(t, err)
	assert.False(t, released)
}

func TestTaskControlDatabaseNowProjectionUsesDialectSyntax(t *testing.T) {
	for _, test := range []struct {
		dialect string
		want    string
	}{
		{dialect: "sqlite", want: "cast(CURRENT_TIMESTAMP as varchar(64))"},
		{dialect: "postgres", want: "cast(CURRENT_TIMESTAMP as varchar(64))"},
		{dialect: "mysql", want: "cast(CURRENT_TIMESTAMP as char(64))"},
	} {
		t.Run(test.dialect, func(t *testing.T) {
			store := NewTaskControlStore(coresql.CreateDb(test.dialect).GetConnection())
			projection, err := store.databaseNowProjection()
			require.NoError(t, err)
			assert.Equal(t, test.want, projection)
		})
	}
}

func TestParseTaskControlDatabaseTimeAcceptsPostgresOffset(t *testing.T) {
	parsed, err := parseTaskControlDatabaseTime("2026-08-29 15:07:08.123456+00")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, time.August, 29, 15, 7, 8, 123456000, time.UTC), parsed)
}

func TestTaskControlTakeoverAtomicallyFencesRecoveryTaskWrites(t *testing.T) {
	database, task, runnerID := controlledTaskFixture(t)
	t.Cleanup(database.Close)
	repository := NewTaskControlStore(database.GetConnection())
	execution, err := pro_interfaces.NewTaskExecutionIdentity(task.ID, runnerID, task.AssignmentGeneration)
	require.NoError(t, err)

	first, claimed, err := repository.ClaimTaskControl(task.ID, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	persisted, err := database.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, first.FencingToken, persisted.TaskControlFencingToken)

	_, err = database.GetConnection().Exec(
		"update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", task.ID,
	)
	require.NoError(t, err)
	second, claimed, err := repository.ClaimTaskControl(task.ID, execution, "boot-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	persisted, err = database.GetTaskByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, second.FencingToken, persisted.TaskControlFencingToken)

	candidate := persisted
	candidate.RecoveryReason = "stale mutation"
	updated, err := database.UpdateTaskFenced(candidate, first.FencingToken)
	require.NoError(t, err)
	assert.False(t, updated)
	candidate.RecoveryReason = "current mutation"
	updated, err = database.UpdateTaskFenced(candidate, second.FencingToken)
	require.NoError(t, err)
	assert.True(t, updated)
}

func TestTaskControlStorePersistsCompleteRunnerSnapshotsAtomically(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	execution, err := pro_interfaces.NewTaskExecutionIdentity(17, 7, 2)
	require.NoError(t, err)
	_, claimed, err := store.ClaimTaskControl(17, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)

	require.NoError(t, store.RecordTaskExecutionSnapshot(7, []coredb.TaskExecutionEvidence{{
		TaskID: 17, Generation: 2, State: coredb.TaskExecutionEvidenceRunning,
		Status: task_logger.TaskRunningStatus,
	}}))
	record, found, err := store.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pro_interfaces.TaskExecutionRunning, record.Evidence.State)
	require.NotNil(t, record.EvidenceObservedAt)

	require.NoError(t, store.RecordTaskExecutionSnapshot(7, []coredb.TaskExecutionEvidence{}))
	record, found, err = store.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pro_interfaces.TaskExecutionAbsent, record.Evidence.State)

	require.NoError(t, store.RecordTaskExecutionSnapshot(7, []coredb.TaskExecutionEvidence{{
		TaskID: 17, Generation: 1, State: coredb.TaskExecutionEvidenceRunning,
		Status: task_logger.TaskRunningStatus,
	}}))
	record, found, err = store.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pro_interfaces.TaskExecutionAbsent, record.Evidence.State)
}

func TestTaskControlStoreFencesRecoveryDecisionAndPreservesTerminalResult(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	execution, err := pro_interfaces.NewTaskExecutionIdentity(17, 7, 2)
	require.NoError(t, err)
	first, claimed, err := store.ClaimTaskControl(17, execution, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, store.RecordTaskExecutionSnapshot(7, []coredb.TaskExecutionEvidence{{
		TaskID: 17, Generation: 2, State: coredb.TaskExecutionEvidenceTerminal,
		Status: task_logger.TaskSuccessStatus,
	}}))

	_, err = database.GetConnection().Exec("update cluster__task_control set lease_expires_at=CURRENT_TIMESTAMP where task_id=?", 17)
	require.NoError(t, err)
	second, claimed, err := store.ClaimTaskControl(17, execution, "boot-b", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	record, found, err := store.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "boot-a", record.PreviousOwnerBootID)
	assert.Equal(t, string(task_logger.TaskSuccessStatus), record.Evidence.TerminalStatus)
	assessment := pro_interfaces.DecideTaskRecovery(second, record.Evidence)
	assert.False(t, assessment.SafeReplacement)
	assert.Equal(t, string(task_logger.TaskSuccessStatus), assessment.TerminalStatus)

	updated, err := store.RecordTaskRecoveryDecision(first, assessment)
	require.NoError(t, err)
	assert.False(t, updated)
	updated, err = store.RecordTaskRecoveryDecision(second, assessment)
	require.NoError(t, err)
	assert.True(t, updated)
}

func TestTaskControlStoreRejectsExecutionIdentityChange(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	first := pro_interfaces.TaskExecutionIdentity{RunnerID: 7, Generation: 2, StableID: "runner-job-17-2"}
	_, claimed, err := store.ClaimTaskControl(17, first, "boot-a", time.Minute)
	require.NoError(t, err)
	assert.True(t, claimed)

	_, claimed, err = store.ClaimTaskControl(17, pro_interfaces.TaskExecutionIdentity{RunnerID: 7, Generation: 3, StableID: "runner-job-17-3"}, "boot-a", time.Minute)
	assert.False(t, claimed)
	assert.ErrorContains(t, err, "execution identity changed")
}

func TestTaskControlStoreAcceptsNewExecutionAfterPriorLeaseEnds(t *testing.T) {
	database := coresql.InitConfigCreateTestStore()
	t.Cleanup(database.Close)
	store := NewTaskControlStore(database.GetConnection())
	first, err := pro_interfaces.NewTaskExecutionIdentity(17, 7, 2)
	require.NoError(t, err)
	lease, claimed, err := store.ClaimTaskControl(17, first, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	released, err := store.ReleaseTaskControlLease(lease)
	require.NoError(t, err)
	require.True(t, released)

	second, err := pro_interfaces.NewTaskExecutionIdentity(17, 8, 3)
	require.NoError(t, err)
	replacement, claimed, err := store.ClaimTaskControl(17, second, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Equal(t, second, replacement.Execution)
	assert.Greater(t, replacement.FencingToken, lease.FencingToken)
	recovery, found, err := store.GetTaskControlRecovery(17)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, pro_interfaces.TaskExecutionUnknown, recovery.Evidence.State)
	assert.Nil(t, recovery.EvidenceObservedAt)
}

func controlledTaskFixture(t *testing.T) (*coresql.SqlDb, coredb.Task, int) {
	t.Helper()
	database := coresql.InitConfigCreateTestStore()
	project, err := database.CreateProject(coredb.Project{Name: "task control"})
	require.NoError(t, err)
	key, err := database.CreateAccessKey(coredb.AccessKey{
		Name: "task control", ProjectID: &project.ID, Type: coredb.AccessKeyNone,
	})
	require.NoError(t, err)
	repository, err := database.CreateRepository(coredb.Repository{
		ProjectID: project.ID, Name: "task control", GitURL: "https://example.com/repo.git",
		GitBranch: "main", SSHKeyID: key.ID,
	})
	require.NoError(t, err)
	inventory, err := database.CreateInventory(coredb.Inventory{ProjectID: project.ID})
	require.NoError(t, err)
	template, err := database.CreateTemplate(coredb.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, InventoryID: &inventory.ID,
		Name: "task control", Playbook: "site.yml", App: coredb.AppBash,
	})
	require.NoError(t, err)
	runner, err := database.CreateRunner(coredb.Runner{
		ProjectID: &project.ID, Name: "task control", Token: coredb.GenerateRunnerToken(),
		Active: true, MaxParallelTasks: 1,
	})
	require.NoError(t, err)
	task, err := database.CreateTask(coredb.Task{
		ProjectID: project.ID, TemplateID: template.ID, Status: task_logger.TaskStartingStatus,
		Playbook: "site.yml", Created: time.Now().UTC(),
	}, 0)
	require.NoError(t, err)
	assigned, ok, err := database.AssignTaskRunner(
		project.ID, task.ID, runner.ID, runner.Name, time.Now().UTC(),
	)
	require.NoError(t, err)
	require.True(t, ok)
	return database, assigned, runner.ID
}
