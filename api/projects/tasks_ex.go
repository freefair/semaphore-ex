package projects

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
	"net/url"
	"strconv"
)

const (
	defaultTaskSummaryPageSize = 50
	maxTaskSummaryPageSize     = 200
)

func parseTaskSummaryPageParams(query url.Values) db.RetrieveQueryParams {
	count := defaultTaskSummaryPageSize
	if raw := query.Get("count"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			count = parsed
		}
	}
	if count > maxTaskSummaryPageSize {
		count = maxTaskSummaryPageSize
	}
	params := db.RetrieveQueryParams{Count: count}
	if raw := query.Get("before"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			params.BeforeID = parsed
		}
	}
	return params
}

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

func taskRecoveryManager(r *http.Request) pro_interfaces.OrphanCleaner {
	manager, _ := helpers.GetFromContext(r, "task_recovery_manager").(pro_interfaces.OrphanCleaner)
	return manager
}

func (c *TaskController) GetTaskRecoveryDiagnostics(w http.ResponseWriter, r *http.Request) {
	manager := taskRecoveryManager(r)
	if manager == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	task := helpers.GetFromContext(r, "task").(db.Task)
	diagnostics, found, err := manager.TaskRecoveryDiagnostics(task.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, diagnostics)
}

func (c *TaskController) RetryTaskRecovery(w http.ResponseWriter, r *http.Request) {
	manager := taskRecoveryManager(r)
	if manager == nil {
		helpers.WriteErrorStatus(w, "HA task recovery is unavailable", http.StatusNotFound)
		return
	}
	task := helpers.GetFromContext(r, "task").(db.Task)
	if err := manager.RetryTaskRecovery(task.ID); err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *TaskController) GetTaskSummary(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	summary, err := c.ansibleTaskRepo.GetTaskSummary(project.ID, task.ID)
	if errors.Is(err, db.ErrNotFound) || err == nil && task.Status.IsFinished() &&
		(summary.State == db.TaskSummaryCollecting || summary.State == db.TaskSummaryPartial) {
		if repairErr := c.ansibleTaskRepo.RepairTaskSummary(project.ID, task.ID, task.Status); repairErr != nil {
			helpers.WriteError(w, repairErr)
			return
		}
		summary, err = c.ansibleTaskRepo.GetTaskSummary(project.ID, task.ID)
	}
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, summary)
}

func (c *TaskController) GetTaskSummaryHosts(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)
	page, err := c.ansibleTaskRepo.GetTaskSummaryHosts(
		project.ID, task.ID, parseTaskSummaryPageParams(r.URL.Query()))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, page)
}

func (c *TaskController) GetTaskSummaryStages(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)
	page, err := c.ansibleTaskRepo.GetTaskSummaryStages(
		project.ID, task.ID, parseTaskSummaryPageParams(r.URL.Query()))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, page)
}

func (c *TaskController) GetTaskSummaryErrors(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)
	page, err := c.ansibleTaskRepo.GetTaskSummaryErrors(
		project.ID, task.ID, parseTaskSummaryPageParams(r.URL.Query()))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, page)
}
