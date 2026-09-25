package main

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	proFactory "github.com/semaphoreui/semaphore/pro/db/factory"
	"github.com/snikch/goodman/hooks"
	trans "github.com/snikch/goodman/transaction"
)

// Each read owns its project, resolved inventory and recorded host execution.
// These routes can be the first transactions in the bundled specification.
func registerInventoryHostDreddFixtures(h *hooks.Hooks) func() {
	registrations := []struct{ path, summary, kind string }{
		{"/api/project/{project_id}/hosts{?inventory_id,search,after_inventory_id,after_host}", "List hosts from the latest complete inventory snapshots", "hosts"},
		{"/api/project/{project_id}/hosts/tasks{?inventory_id,host,before}", "List tasks that recorded execution on one inventory host", "tasks"},
		{"/api/project/{project_id}/inventory/{inventory_id}/hosts/snapshots", "Read recent inventory resolution attempts", "snapshots"},
	}
	configure := []func(){}
	for _, registration := range registrations {
		name := fmt.Sprintf("inventory > %s > %s > 200 > application/json", registration.path, registration.summary)
		var fixtureTask db.Task
		var fixtureInventory db.Inventory
		h.Before(name, func(t *trans.Transaction) {
			addCapabilities([]string{"project", "task"})
			fixtureTask = *task
			dbConnect()
			defer store.Close()
			var err error
			fixtureInventory, err = store.GetInventory(userProject.ID, inventoryID)
			printError(err)
			repository := proFactory.NewAnsibleTaskRepository(store)
			hosts := repository.(db.InventoryHostRepository)
			printError(hosts.IngestInventoryHostEvent(fixtureTask, fixtureInventory, db.InventoryHostEvent{Version: 1, Kind: "host", Host: "web-01", Groups: []string{"all"}}))
			printError(hosts.IngestInventoryHostEvent(fixtureTask, fixtureInventory, db.InventoryHostEvent{Version: 1, Kind: "complete", Count: 1}))
			printError(repository.IngestTaskSummaryEvent(userProject.ID, fixtureTask.ID, db.TaskSummaryEvent{
				Version: 1, Kind: db.TaskSummaryEventHost, EventID: "dredd-host", Host: "web-01", Status: "ok", Ok: 1,
			}, fixtureTask.Created))
		})
		configure = append(configure, func() {
			h.Before(name, func(t *trans.Transaction) {
				uri := fmt.Sprintf("/api/project/%d/hosts", fixtureInventory.ProjectID)
				switch registration.kind {
				case "tasks":
					uri += fmt.Sprintf("/tasks?inventory_id=%d&host=web-01", fixtureInventory.ID)
				case "snapshots":
					uri = fmt.Sprintf("/api/project/%d/inventory/%d/hosts/snapshots", fixtureInventory.ProjectID, fixtureInventory.ID)
				}
				t.FullPath, t.Request.URI = uri, uri
			})
		})
		h.After(name, func(t *trans.Transaction) {
			if t.Real == nil || t.Real.StatusCode != 200 {
				return
			}
			switch registration.kind {
			case "hosts":
				var page db.InventoryHostPage
				if json.Unmarshal([]byte(t.Real.Body), &page) != nil || len(page.Items) != 1 || page.Items[0].Host != "web-01" || page.Items[0].InventoryID != fixtureInventory.ID {
					t.Fail = "host response did not contain the resolved fixture inventory"
				}
			case "tasks":
				var page db.TaskSummaryPage[db.TaskWithTpl]
				if json.Unmarshal([]byte(t.Real.Body), &page) != nil || len(page.Items) != 1 || page.Items[0].ID != fixtureTask.ID {
					t.Fail = "host history did not contain the recorded fixture execution"
				}
			case "snapshots":
				var snapshots []db.InventoryHostSnapshot
				if json.Unmarshal([]byte(t.Real.Body), &snapshots) != nil || len(snapshots) != 1 || snapshots[0].TaskID != fixtureTask.ID || snapshots[0].State != "ready" {
					t.Fail = "snapshot response did not contain the complete fixture resolution"
				}
			}
		})
	}
	return func() {
		for _, register := range configure {
			register()
		}
	}
}
