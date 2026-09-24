package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryHostsPublishCompleteSnapshotsAndRetainLastSuccess(t *testing.T) {
	fixture := newSummaryFixture(t, "inventory membership")
	repository := fixture.repository.(db.InventoryHostRepository)
	template, err := fixture.store.GetTemplate(fixture.projectID, fixture.task.TemplateID)
	require.NoError(t, err)
	inventory, err := fixture.store.GetInventory(fixture.projectID, *template.InventoryID)
	require.NoError(t, err)
	host := db.InventoryHostEvent{Version: 1, Kind: "host", Host: "web_01", Groups: []string{"all", "web"}}
	// Split runner reports may arrive out of order and may be replayed.
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 2}))
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, host))
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, host))
	page, err := repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "partial inventory must not be published")
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: "never-run", Groups: []string{"all"}}))
	page, err = repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.Equal(t, "never-run", page.Items[0].Host)
	assert.Equal(t, db.StringArrayField{"all", "web"}, page.Items[1].Groups)
	ids, err := repository.GetHostTaskIDs(fixture.projectID, inventory.ID, "never-run", 0, 20)
	require.NoError(t, err)
	assert.Empty(t, ids, "membership must not claim execution")
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID, db.TaskSummaryEvent{Version: 1, Kind: db.TaskSummaryEventHost, EventID: "host:web_01", Host: "web_01", Status: "success", Ok: 1}, time.Now()))
	ids, err = repository.GetHostTaskIDs(fixture.projectID, inventory.ID, "web_01", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, []int{fixture.task.ID}, ids)
	second, err := fixture.store.CreateTask(db.Task{ProjectID: fixture.projectID, TemplateID: template.ID, Created: time.Now(), Status: task_logger.TaskRunningStatus}, 0)
	require.NoError(t, err)
	require.NoError(t, repository.IngestInventoryHostEvent(second, inventory, db.InventoryHostEvent{Version: 1, Kind: "error"}))
	page, err = repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{})
	require.NoError(t, err)
	assert.Len(t, page.Items, 2, "failure retains previous complete membership")
	snapshots, err := repository.GetInventoryHostSnapshots(fixture.projectID, inventory.ID)
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	assert.Equal(t, "error", snapshots[0].State)
}

func TestInventoryHostQueriesAreScopedLiteralAndPaginated(t *testing.T) {
	fixture := newSummaryFixture(t, "host query boundaries")
	repository := fixture.repository.(db.InventoryHostRepository)
	template, err := fixture.store.GetTemplate(fixture.projectID, fixture.task.TemplateID)
	require.NoError(t, err)
	inventory, err := fixture.store.GetInventory(fixture.projectID, *template.InventoryID)
	require.NoError(t, err)
	for _, host := range []string{"app_01", "appX01", "web"} {
		require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: host, Groups: []string{}}))
	}
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 3}))
	page, err := repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{Search: "_"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "app_01", page.Items[0].Host)
	page, err = repository.GetInventoryHosts(fixture.projectID+1, db.InventoryHostQuery{})
	require.NoError(t, err)
	assert.Empty(t, page.Items)
	page, err = repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{Count: 2})
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	assert.NotEmpty(t, page.NextHost)
	last, err := repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{Count: 2, AfterHost: page.NextHost, AfterInventoryID: page.NextInventoryID})
	require.NoError(t, err)
	require.Len(t, last.Items, 1)
	assert.Equal(t, "web", last.Items[0].Host)
	stale := fixture.task
	stale.AssignmentGeneration++
	assert.ErrorIs(t, repository.IngestInventoryHostEvent(stale, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete"}), db.ErrNotFound)
}

func TestHostHistoryUsesImmutableInventoryForOlderReviewedRuns(t *testing.T) {
	fixture := newSummaryFixture(t, "older host history")
	repository := fixture.repository.(db.InventoryHostRepository)
	template, err := fixture.store.GetTemplate(fixture.projectID, fixture.task.TemplateID)
	require.NoError(t, err)
	inventory, err := fixture.store.GetInventory(fixture.projectID, *template.InventoryID)
	require.NoError(t, err)
	repo, err := fixture.store.GetRepository(fixture.projectID, template.RepositoryID)
	require.NoError(t, err)
	encoded, err := db.EncodeTaskExecutionSnapshot(db.TaskExecutionSnapshot{
		Version: db.TaskExecutionSnapshotVersion, ProjectID: fixture.projectID,
		Fingerprint: "historical-review", Template: template, Inventory: &inventory, Repository: repo,
	})
	require.NoError(t, err)
	fixture.task, err = fixture.store.CreateTask(db.Task{
		ProjectID: fixture.projectID, TemplateID: template.ID, Created: time.Now(),
		Status: task_logger.TaskRunningStatus, ExecutionSnapshotJSON: &encoded,
	}, 0)
	require.NoError(t, err)
	require.NoError(t, fixture.repository.IngestTaskSummaryEvent(fixture.projectID, fixture.task.ID,
		db.TaskSummaryEvent{Version: 1, Kind: db.TaskSummaryEventHost, EventID: "old-host", Host: "web", Status: "success", Ok: 1}, time.Now()))
	other, err := fixture.store.CreateInventory(db.Inventory{ProjectID: fixture.projectID, Name: "new selection", Type: db.InventoryStatic, Inventory: "web"})
	require.NoError(t, err)
	template.InventoryID = &other.ID
	require.NoError(t, fixture.store.UpdateTemplate(template))
	ids, err := repository.GetHostTaskIDs(fixture.projectID, inventory.ID, "web", 0, 20)
	require.NoError(t, err)
	assert.Equal(t, []int{fixture.task.ID}, ids)
	ids, err = repository.GetHostTaskIDs(fixture.projectID, other.ID, "web", 0, 20)
	require.NoError(t, err)
	assert.Empty(t, ids, "editing the template cannot move an old host execution to a different inventory")
}

func TestHostBrowserUsesCurrentInventoryNamesAndOmitsDeletedInventories(t *testing.T) {
	fixture := newSummaryFixture(t, "inventory lifecycle")
	repository := fixture.repository.(db.InventoryHostRepository)
	template, err := fixture.store.GetTemplate(fixture.projectID, fixture.task.TemplateID)
	require.NoError(t, err)
	inventory, err := fixture.store.GetInventory(fixture.projectID, *template.InventoryID)
	require.NoError(t, err)
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: "web", Groups: []string{}}))
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 1}))
	inventory.Name = "Renamed inventory"
	require.NoError(t, fixture.store.UpdateInventory(inventory))
	page, err := repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{Search: "Renamed"})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "Renamed inventory", page.Items[0].InventoryName)
	other, err := fixture.store.CreateInventory(db.Inventory{ProjectID: fixture.projectID, Name: "replacement", Type: db.InventoryStatic, Inventory: "web"})
	require.NoError(t, err)
	template.InventoryID = &other.ID
	require.NoError(t, fixture.store.UpdateTemplate(template))
	require.NoError(t, fixture.store.DeleteInventory(fixture.projectID, inventory.ID))
	page, err = repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{})
	require.NoError(t, err)
	assert.Empty(t, page.Items, "the current host browser must not link to deleted inventory details")
}

func TestInventoryHostSnapshotsIgnoreFramesAfterTerminalState(t *testing.T) {
	fixture := newSummaryFixture(t, "sealed inventory membership")
	repository := fixture.repository.(db.InventoryHostRepository)
	template, err := fixture.store.GetTemplate(fixture.projectID, fixture.task.TemplateID)
	require.NoError(t, err)
	inventory, err := fixture.store.GetInventory(fixture.projectID, *template.InventoryID)
	require.NoError(t, err)

	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: "trusted", Groups: []string{"all"}}))
	require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 1}))
	for _, injected := range []db.InventoryHostEvent{
		{Version: 1, Kind: "host", Host: "injected", Groups: []string{"prod"}},
		{Version: 1, Kind: "complete", Count: 0},
		{Version: 1, Kind: "error"},
	} {
		require.NoError(t, repository.IngestInventoryHostEvent(fixture.task, inventory, injected))
	}
	page, err := repository.GetInventoryHosts(fixture.projectID, db.InventoryHostQuery{InventoryID: inventory.ID})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "trusted", page.Items[0].Host)

	failed, err := fixture.store.CreateTask(db.Task{ProjectID: fixture.projectID, TemplateID: template.ID, Created: time.Now(), Status: task_logger.TaskRunningStatus}, 0)
	require.NoError(t, err)
	require.NoError(t, repository.IngestInventoryHostEvent(failed, inventory, db.InventoryHostEvent{Version: 1, Kind: "error"}))
	require.NoError(t, repository.IngestInventoryHostEvent(failed, inventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: "late", Groups: []string{"all"}}))
	require.NoError(t, repository.IngestInventoryHostEvent(failed, inventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 1}))
	snapshots, err := repository.GetInventoryHostSnapshots(fixture.projectID, inventory.ID)
	require.NoError(t, err)
	require.Len(t, snapshots, 2)
	assert.Equal(t, "error", snapshots[0].State)
	assert.Equal(t, -1, snapshots[0].HostCount, "late complete frames must not change an error snapshot")
}
