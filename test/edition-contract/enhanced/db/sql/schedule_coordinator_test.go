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
	var terminalOutcome string
	require.NoError(t, store.GetConnection().SelectOne(&terminalOutcome, "select terminal_outcome from cluster__schedule_occurrence where occurrence_key=?", occurrence.Key))
	assert.Equal(t, "completed", terminalOutcome, "a successfully bound task must terminalize the occurrence")
	_, acquired, err = repository.ClaimScheduleOccurrence(occurrence, "boot-c", time.Minute)
	require.NoError(t, err)
	assert.False(t, acquired)
}

func TestScheduleCoordinatorBlocksOnlyItsCurrentImmutableDecision(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	repository := NewScheduleCoordinatorStore(store.GetConnection())
	project, err := store.CreateProject(db.Project{Name: "blocked schedule"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Type: db.AccessKeyNone})
	require.NoError(t, err)
	repositoryResource, err := store.CreateRepository(db.Repository{ProjectID: project.ID, SSHKeyID: key.ID, Name: "repository", GitURL: "https://example.invalid/repo.git", GitBranch: "main"})
	require.NoError(t, err)
	projectID := project.ID
	template, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryResource.ID, Name: "blocked", Playbook: "site.yml"})
	require.NoError(t, err)
	schedule, err := store.CreateSchedule(db.Schedule{ProjectID: projectID, TemplateID: template.ID, Name: "blocked", CronFormat: "0 9 * * *", Active: true})
	require.NoError(t, err)
	occurrence, err := pro_interfaces.NewScheduleOccurrence(schedule.ID, "revision", time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	lease, acquired, err := repository.ClaimScheduleOccurrence(occurrence, "boot-a", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	decisionKey, err := pro_interfaces.DeploymentWindowScheduleDecisionKey(occurrence)
	require.NoError(t, err)
	now := time.Now().UTC()
	result, err := store.GetConnection().Exec(
		"insert into project__deployment_window_decision(project_id, decision_key, source, origin, template_id, schedule_id, policy_revision, effective_timezone, evaluated_at, state, reason, next_eligible_known, matched_rules, created) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		projectID, decisionKey, "schedule", "schedule", template.ID, schedule.ID, 1, "UTC", now, "blocked", "freeze_active", true, "[]", now,
	)
	require.NoError(t, err)
	decisionID64, err := result.LastInsertId()
	require.NoError(t, err)
	decisionID := int(decisionID64)

	blocked, err := repository.BlockScheduleOccurrence(lease, decisionID)
	require.NoError(t, err)
	assert.True(t, blocked)
	blocked, err = repository.BlockScheduleOccurrence(lease, decisionID)
	require.NoError(t, err)
	assert.True(t, blocked, "repeating the exact terminal transition is idempotent")
	_, acquired, err = repository.ClaimScheduleOccurrence(occurrence, "boot-b", time.Minute)
	require.NoError(t, err)
	assert.False(t, acquired, "a terminal blocked occurrence is never re-claimed")

	other, err := pro_interfaces.NewScheduleOccurrence(schedule.ID, "revision", occurrence.IntendedAt.Add(time.Minute))
	require.NoError(t, err)
	otherLease, acquired, err := repository.ClaimScheduleOccurrence(other, "boot-c", time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	blocked, err = repository.BlockScheduleOccurrence(otherLease, decisionID)
	require.NoError(t, err)
	assert.False(t, blocked, "a decision for another occurrence cannot terminalize this one")
}
