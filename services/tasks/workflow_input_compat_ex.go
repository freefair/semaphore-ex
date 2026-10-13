package tasks

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

// AddFailedTask is intentionally unavailable for workflow tasks in EX. The
// upstream helper creates a row outside the workflow reconciliation lease,
// which would bypass HA fencing and cross-project provenance. Enhanced input
// resolution instead performs the authoritative fenced node failure.
func (p *TaskPool) AddFailedTask(task db.Task, _ *int, _ string, _ []string) (db.Task, error) {
	if task.WorkflowRunID != nil {
		return db.Task{}, errors.New("unfenced workflow failed-task creation is unavailable")
	}
	return db.Task{}, errors.New("failed-task creation is unavailable")
}

// LogTask uses the normal log channel even before asynchronous pool registration.
// A temporary logger does not create, start or persist another task.
func (p *TaskPool) LogTask(task db.Task, lines []string) {
	if p == nil || len(lines) == 0 {
		return
	}
	runner, err := p.GetTask(task.ID)
	if err != nil || runner == nil {
		runner = NewTaskRunner(task, p, "", p.keyInstallationService)
		if template, err := p.store.GetTemplate(task.ProjectID, task.TemplateID); err == nil {
			runner.Template = template
		}
	}
	for _, line := range lines {
		runner.Log(line)
	}
}
