package projects

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

// GetTaskRunnerAttempts returns the immutable runner assignment history for a task.
func (c *TaskController) GetTaskRunnerAttempts(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	task := helpers.GetFromContext(r, "task").(db.Task)
	attempts, err := c.store.GetTaskRunnerAttempts(project.ID, task.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, attempts)
}
