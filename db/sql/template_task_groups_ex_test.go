package sql

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTemplateTaskGroupPolicyRunner(t *testing.T, store *SqlDb, projectID int, name string, tags []string, active bool) db.Runner {
	t.Helper()
	runner, err := store.CreateRunner(db.Runner{
		ProjectID: &projectID, Name: name, Tags: tags, Active: active,
		Token: db.GenerateRunnerToken(),
	})
	require.NoError(t, err)
	return runner
}

func createTemplateTaskGroupPolicyGroup(t *testing.T, store *SqlDb, projectID, runnerID int) db.TaskGroup {
	t.Helper()
	group, err := store.CreateTaskGroup(db.TaskGroup{
		ProjectID: projectID, Name: "restricted runner", MaxParallelTasks: 1,
		RunnerIDs: db.TaskGroupBindings{runnerID},
	})
	require.NoError(t, err)
	return group
}

func TestTemplateTaskGroupsRejectConflictingRunnerTags(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runnerA := createTemplateTaskGroupPolicyRunner(t, store, projectID, "runner-a", []string{"linux-a"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runnerA.ID)

	_, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "conflicting", Playbook: "site.yml",
		RunnerTags: db.StringArrayField{"linux-b"}, TaskGroups: db.TaskGroupBindings{group.ID},
	})
	assert.ErrorContains(t, err, taskGroupRunnerPolicyConflictMessage)
}

func TestTemplateTaskGroupsRejectConflictingRunnerTagsOnUpdate(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runnerA := createTemplateTaskGroupPolicyRunner(t, store, projectID, "runner-a", []string{"linux-a"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runnerA.ID)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "compatible", Playbook: "site.yml",
		RunnerTags: db.StringArrayField{"linux-a"}, TaskGroups: db.TaskGroupBindings{group.ID},
	})
	require.NoError(t, err)

	template.RunnerTags = db.StringArrayField{"linux-b"}
	assert.ErrorContains(t, store.UpdateTemplate(template), taskGroupRunnerPolicyConflictMessage)
	reloaded, err := store.GetTemplate(projectID, template.ID)
	require.NoError(t, err)
	assert.Equal(t, db.StringArrayField{"linux-a"}, reloaded.RunnerTags)
}

func TestTaskGroupPolicyUpdateRejectsConflictingTemplateRunnerTags(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runnerA := createTemplateTaskGroupPolicyRunner(t, store, projectID, "runner-a", []string{"linux-a"}, true)
	runnerB := createTemplateTaskGroupPolicyRunner(t, store, projectID, "runner-b", []string{"linux-b"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runnerA.ID)
	_, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "compatible", Playbook: "site.yml",
		RunnerTags: db.StringArrayField{"linux-a"}, TaskGroups: db.TaskGroupBindings{group.ID},
	})
	require.NoError(t, err)

	group.RunnerIDs = db.TaskGroupBindings{runnerB.ID}
	_, err = store.UpdateTaskGroup(group)
	assert.ErrorContains(t, err, taskGroupRunnerPolicyConflictMessage)
	reloaded, err := store.GetTaskGroup(projectID, group.ID)
	require.NoError(t, err)
	assert.Equal(t, db.TaskGroupBindings{runnerA.ID}, reloaded.RunnerIDs)
}

func TestTemplateTaskGroupRunnerPolicyIgnoresRunnerLiveness(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runner := createTemplateTaskGroupPolicyRunner(t, store, projectID, "offline-compatible", []string{"linux-a"}, false)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runner.ID)

	_, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "offline-compatible", Playbook: "site.yml",
		RunnerTags: db.StringArrayField{"linux-a"}, TaskGroups: db.TaskGroupBindings{group.ID},
	})
	require.NoError(t, err)
}

func TestTemplateTaskGroupRunnerPolicyAllowsVisibleNonDefaultRunnerWithoutTags(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runner := createTemplateTaskGroupPolicyRunner(t, store, projectID, "non-default", []string{"linux"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runner.ID)

	_, err := store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "explicit group runner", Playbook: "site.yml",
		TaskGroups: db.TaskGroupBindings{group.ID},
	})
	require.NoError(t, err)
}

func TestTemplateTaskGroupRunnerPolicyRejectsInvisibleSharedGroupRunnerWithoutTags(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	ownerProjectID, _ := newTemplateTestProject(t, store)
	consumerProjectID, consumerRepositoryID := newTemplateTestProject(t, store)
	runner := createTemplateTaskGroupPolicyRunner(t, store, ownerProjectID, "owner-only", []string{"linux"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, ownerProjectID, runner.ID)
	// Model a stale direct grant from an older or externally modified database:
	// catalog mutation validation prevents creating this invalid share today.
	_, err := store.exec("insert into project__task_group_grant(group_id,project_id) values (?,?)", group.ID, consumerProjectID)
	require.NoError(t, err)

	_, err = store.CreateTemplate(db.Template{
		ProjectID: consumerProjectID, RepositoryID: consumerRepositoryID,
		Name: "invisible runner", Playbook: "site.yml", TaskGroups: db.TaskGroupBindings{group.ID},
	})
	assert.ErrorContains(t, err, taskGroupRunnerPolicyConflictMessage)
}

func TestTemplateTaskGroupsApplyInventoryRunnerTagFallback(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runner := createTemplateTaskGroupPolicyRunner(t, store, projectID, "runner-a", []string{"linux-a"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runner.ID)
	inventoryTag := "linux-b"
	inventory, err := store.CreateInventory(db.Inventory{
		ProjectID: projectID, Name: "inventory", Type: db.InventoryStatic, Inventory: "localhost",
		RunnerTag: &inventoryTag,
	})
	require.NoError(t, err)

	_, err = store.CreateTemplate(db.Template{
		ProjectID: projectID, RepositoryID: repositoryID, InventoryID: &inventory.ID,
		Name: "inventory-conflict", Playbook: "site.yml", TaskGroups: db.TaskGroupBindings{group.ID},
	})
	assert.ErrorContains(t, err, taskGroupRunnerPolicyConflictMessage)
}

func TestTemplateTaskGroupWriteIsSerializedWithGroupDeletion(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	projectID, repositoryID := newTemplateTestProject(t, store)
	runner := createTemplateTaskGroupPolicyRunner(t, store, projectID, "runner", []string{"linux"}, true)
	group := createTemplateTaskGroupPolicyGroup(t, store, projectID, runner.ID)

	tx, err := store.Sql().Begin()
	require.NoError(t, err)
	require.NoError(t, store.lockTaskGroupDispatchTx(tx))
	started := make(chan struct{})
	createResult := make(chan error, 1)
	go func() {
		close(started)
		_, createErr := store.CreateTemplate(db.Template{
			ProjectID: projectID, RepositoryID: repositoryID, Name: "racing template", Playbook: "site.yml",
			RunnerTags: db.StringArrayField{"linux"}, TaskGroups: db.TaskGroupBindings{group.ID},
		})
		createResult <- createErr
	}()
	<-started
	_, err = tx.Exec(store.PrepareQuery("delete from project__task_group where id=?"), group.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	assert.ErrorContains(t, <-createResult, "task group selection is invalid")
	var count int
	require.NoError(t, store.selectOne(&count, "select count(*) from project__template where project_id=? and name=?", projectID, "racing template"))
	assert.Zero(t, count, "a template write that lost the group-deletion race must not persist a dangling membership")
}
