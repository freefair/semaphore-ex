package main

import (
	"fmt"
	"github.com/semaphoreui/semaphore/tools/dreddhooks"

	"github.com/semaphoreui/semaphore/db"
	"github.com/snikch/goodman/hooks"
	trans "github.com/snikch/goodman/transaction"
)

// Register after generic path substitution: each transaction owns a fresh group
// and revision rather than depending on the order of earlier HTTP mutations.
func registerTaskGroupDreddFixtures(h *hooks.Hooks) {
	registrations := []struct {
		path, summary, status string
		collection            bool
	}{
		{"/api/project/{project_id}/task_groups", "List owned and explicitly shared task groups", "200", true},
		{"/api/project/{project_id}/task_groups", "Create a project-owned task group", "201", true},
		{"/api/project/{project_id}/task_groups/{group_id}", "Read an owned or explicitly shared task group", "200", false},
		{"/api/project/{project_id}/task_groups/{group_id}", "Update a task group using its current revision", "204", false},
		{"/api/project/{project_id}/task_groups/{group_id}", "Delete an unreferenced task group", "204", false},
	}
	for _, registration := range registrations {
		h.Before(fmt.Sprintf("project > %s > %s > %s > application/json", registration.path, registration.summary, registration.status), func(t *trans.Transaction) {
			if t.Skip {
				return
			}
			dbConnect()
			defer store.Close()
			groups, ok := store.(db.TaskGroupManager)
			if !ok {
				panic("Dredd store does not implement task groups")
			}
			group, err := groups.CreateTaskGroup(db.TaskGroup{ProjectID: userProject.ID, Name: "Dredd group " + getUUID(), MaxParallelTasks: 1})
			if err != nil {
				panic(fmt.Errorf("create task group fixture: %w", err))
			}
			configureTaskGroupDreddRequest(t, group, registration.collection)
		})
	}
}

func configureTaskGroupDreddRequest(t *trans.Transaction, group db.TaskGroup, collection bool) {
	uri, body, err := dreddhooks.TaskGroupRequest(group, t.Request.Method, collection)
	if err != nil {
		panic(err)
	}
	t.FullPath = uri
	t.Request.URI = uri
	if body != "" {
		t.Request.Body = body
	}
}
