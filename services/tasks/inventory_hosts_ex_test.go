package tasks

import (
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
)

type inventoryHostResultRepositoryStub struct {
	taskSummaryRepositoryStub
	events []db.InventoryHostEvent
}

var _ db.InventoryHostRepository = (*inventoryHostResultRepositoryStub)(nil)

func (r *inventoryHostResultRepositoryStub) IngestInventoryHostEvent(_ db.Task, _ db.Inventory, event db.InventoryHostEvent) error {
	r.events = append(r.events, event)
	return nil
}

func (*inventoryHostResultRepositoryStub) GetInventoryHostSnapshots(int, int) ([]db.InventoryHostSnapshot, error) {
	return nil, nil
}

func (*inventoryHostResultRepositoryStub) GetInventoryHosts(int, db.InventoryHostQuery) (db.InventoryHostPage, error) {
	return db.InventoryHostPage{}, nil
}

func (*inventoryHostResultRepositoryStub) GetHostTaskIDs(int, int, string, int, int) ([]int, error) {
	return nil, nil
}

func TestCaptureInventoryHostResultRejectsAmbiguousAndOversizedFrames(t *testing.T) {
	repository := &inventoryHostResultRepositoryStub{}
	_, supported := any(repository).(db.InventoryHostRepository)
	assert.True(t, supported)
	runner := &TaskRunner{
		pool:     &TaskPool{ansibleTaskRepo: repository},
		Task:     db.Task{ID: 11, ProjectID: 22},
		Template: db.Template{App: db.AppAnsible},
	}
	valid := `SEMAPHORE_INVENTORY_RESULT {"version":1,"event":"host","host":"web","groups":["all"]}`

	assert.True(t, runner.captureInventoryHostResult(valid+` {"version":1,"event":"complete","count":1}`))
	assert.Empty(t, repository.events, "a framed result must contain exactly one JSON document")

	overflow := `SEMAPHORE_INVENTORY_RESULT {"version":1,"event":"host","host":"web","groups":["` +
		strings.Repeat("a", maxInventoryHostResultBytes) + `"]}`
	assert.True(t, runner.captureInventoryHostResult(overflow))
	assert.Empty(t, repository.events, "payload bounds must apply before JSON allocation")

	assert.True(t, runner.captureInventoryHostResult(valid))
	assert.Equal(t, []db.InventoryHostEvent{{Version: 1, Kind: "host", Host: "web", Groups: []string{"all"}}}, repository.events)
}
