package tasks

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/util"
)

func (t *TaskRunner) publishStatus() {
	for _, user := range t.users {
		b, err := json.Marshal(&map[string]any{
			"type":                  "update",
			"start":                 t.Task.Start,
			"end":                   t.Task.End,
			"status":                t.Task.Status,
			"task_id":               t.Task.ID,
			"template_id":           t.Task.TemplateID,
			"project_id":            t.Task.ProjectID,
			"version":               t.Task.Version,
			"assignment_generation": t.Task.AssignmentGeneration,
			"runner_assigned_at":    t.Task.RunnerAssignedAt,
			"recovery_reason":       t.Task.RecoveryReason,
		})

		util.LogPanic(err)

		sockets.Message(user, b)
	}
}
