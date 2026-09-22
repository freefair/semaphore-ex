package dreddhooks

import (
	"encoding/json"
	"fmt"
	"github.com/semaphoreui/semaphore/db"
)

// TaskGroupRequest binds each positive API contract transaction to its own
// persisted identity and revision, independent of transaction execution order.
func TaskGroupRequest(group db.TaskGroup, method string, collection bool) (string, string, error) {
	uri := fmt.Sprintf("/api/project/%d/task_groups", group.ProjectID)
	if !collection {
		uri += fmt.Sprintf("/%d", group.ID)
	}
	var body any
	switch method {
	case "POST", "PUT":
		body = map[string]any{"name": group.Name + " updated", "description": "Dredd managed group", "max_parallel_tasks": 2, "runner_ids": []int{}, "shared_project_ids": []int{}, "revision": group.Revision}
	case "DELETE":
		body = map[string]int{"revision": group.Revision}
	default:
		return uri, "", nil
	}
	encoded, err := json.Marshal(body)
	return uri, string(encoded), err
}
