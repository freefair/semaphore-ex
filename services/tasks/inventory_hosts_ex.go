package tasks

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	log "github.com/sirupsen/logrus"
)

const maxInventoryHostResultBytes = 64 * 1024

func (t *TaskRunner) captureInventoryHostResult(message string) bool {
	const prefix = "SEMAPHORE_INVENTORY_RESULT "
	message = strings.TrimSpace(message)
	if !strings.HasPrefix(message, prefix) {
		return false
	}
	payload := strings.TrimPrefix(message, prefix)
	if len(payload) > maxInventoryHostResultBytes {
		log.WithFields(log.Fields{"task_id": t.Task.ID, "project_id": t.Task.ProjectID}).Warn("Inventory host result could not be stored")
		return true
	}
	var event db.InventoryHostEvent
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&event)
	if err == nil {
		if trailingErr := decoder.Decode(&struct{}{}); !errors.Is(trailingErr, io.EOF) {
			err = errors.New("inventory host result must contain one JSON document")
		}
	}
	if err == nil {
		err = event.Validate()
	}
	if err == nil && t.pool != nil {
		if repository, ok := t.pool.ansibleTaskRepo.(db.InventoryHostRepository); ok {
			err = repository.IngestInventoryHostEvent(t.Task, t.Inventory, event)
		}
	}
	if err != nil {
		// Do not echo a decoder error: it may contain source-controlled values.
		log.WithFields(log.Fields{"task_id": t.Task.ID, "project_id": t.Task.ProjectID}).Warn("Inventory host result could not be stored")
	}
	return true
}
