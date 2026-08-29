package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleCoordinatorStoreFencesExpiredLeaseOwners(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewScheduleCoordinatorStore(store.GetConnection())
	occurrence, err := pro_interfaces.NewScheduleOccurrence(42, "schedule-revision-a", time.Date(2026, 8, 29, 12, 34, 0, 0, time.UTC))
	require.NoError(t, err)

	first, acquired, err := repository.ClaimScheduleOccurrence(occurrence, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	assert.EqualValues(t, 1, first.FencingToken)

	_, err = store.GetConnection().Exec("update cluster__schedule_occurrence set lease_expires_at=CURRENT_TIMESTAMP where occurrence_key=?", occurrence.Key)
	require.NoError(t, err)
	second, acquired, err := repository.ClaimScheduleOccurrence(occurrence, "boot-b", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	assert.Greater(t, second.FencingToken, first.FencingToken)

	current, err := repository.IsCurrentScheduleLease(first)
	require.NoError(t, err)
	assert.False(t, current)
	released, err := repository.ReleaseScheduleOccurrenceLease(first)
	require.NoError(t, err)
	assert.False(t, released)
	current, err = repository.IsCurrentScheduleLease(second)
	require.NoError(t, err)
	assert.True(t, current)
	project, err := store.CreateProject(db.Project{Name: "schedule coordinator"})
	require.NoError(t, err)
	accessKey, err := store.CreateAccessKey(db.AccessKey{Name: "key", ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repositoryRecord, err := store.CreateRepository(db.Repository{ProjectID: project.ID, Name: "repo", GitURL: "https://example.com/repo.git", GitBranch: "main", SSHKeyID: accessKey.ID})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{ProjectID: project.ID, RepositoryID: repositoryRecord.ID, Name: "template", Playbook: "site.yml", App: db.AppBash})
	require.NoError(t, err)
	task, err := store.CreateTask(db.Task{TemplateID: template.ID, ProjectID: project.ID, Status: task_logger.TaskWaitingStatus, Playbook: "site.yml", ScheduleOccurrenceKey: &occurrence.Key, Created: time.Now()}, 0)
	require.NoError(t, err)
	completed, err := repository.CompleteScheduleOccurrence(second, task.ID)
	require.NoError(t, err)
	assert.True(t, completed)
	_, acquired, err = repository.ClaimScheduleOccurrence(occurrence, "boot-c", time.Minute)
	require.NoError(t, err)
	assert.False(t, acquired)
}
