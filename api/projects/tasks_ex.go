package projects

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	log "github.com/sirupsen/logrus"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultTaskSummaryPageSize     = 50
	maxTaskSummaryPageSize         = 200
	taskPreflightFingerprintHeader = "X-Semaphore-Preflight-Fingerprint"
	taskPreflightTokenHeader       = "X-Semaphore-Preflight-Token"
)

// ConfigureExecutionPreflightAudit attaches the optional value-free audit
// recorder after route construction. Community route wiring may omit it.
func (c *TaskController) ConfigureExecutionPreflightAudit(audit pro_interfaces.AuditServiceFacade) {
	c.audit = audit
}

// PreviewTask returns the value-free execution plan that AddTask recomputes
// immediately before enqueue. Route middleware provides the same project,
// template, and task-run authorization as AddTask.
func (c *TaskController) PreviewTask(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	user := helpers.GetFromContext(r, "user").(*db.User)
	taskObj := helpers.GetFromContext(r, "task").(db.Task)
	plan, err := taskPool(r).PreviewTaskExecution(taskObj, user, project.ID)
	if err != nil {
		log.WithFields(log.Fields{
			"context": "PreviewTask", "project_id": project.ID,
			"template_id": taskObj.TemplateID, "user_id": user.ID,
		}).WithError(err).Error("Cannot preview task execution")
		helpers.WriteErrorStatus(w, "EXECUTION_PREFLIGHT_UNAVAILABLE", http.StatusServiceUnavailable)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, plan)
	outcome, reason := pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonExecutionPreflightPreviewed
	if denialReason, denied := pro_interfaces.ExecutionPreflightAuditDenialReason(plan); denied {
		outcome, reason = pro_interfaces.AuditOutcomeDenied, denialReason
	}
	c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightPreview,
		outcome, reason,
		pro_interfaces.ExecutionPreflightTask, taskObj.TemplateID, &plan, nil)
}

func (c *TaskController) writeTaskExecutionPreflightError(w http.ResponseWriter, r *http.Request, taskObj db.Task, planned pro_interfaces.ExecutionPreflightPlan, err error) bool {
	if err == nil {
		return false
	}
	var stale *tasks.ExecutionPreflightStaleError
	if errors.As(err, &stale) {
		c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
			pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonExecutionPreflightStale,
			pro_interfaces.ExecutionPreflightTask, taskObj.TemplateID, &stale.Preflight, stale.Changes)
		helpers.WriteJSON(w, http.StatusConflict, pro_interfaces.ExecutionPreflightStale{
			Code: "stale_execution_preflight", Changes: stale.Changes, Preflight: &stale.Preflight,
		})
		return true
	}
	var denied *tasks.ExecutionPreflightDeniedError
	if errors.As(err, &denied) {
		reason := pro_interfaces.AuditReasonExecutionPreflightDenied
		if denialReason, found := pro_interfaces.ExecutionPreflightAuditDenialReason(denied.Preflight); found {
			reason = denialReason
		}
		c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
			pro_interfaces.AuditOutcomeDenied, reason,
			pro_interfaces.ExecutionPreflightTask, taskObj.TemplateID, &denied.Preflight, nil)
		helpers.WriteJSON(w, http.StatusConflict, pro_interfaces.ExecutionPreflightStale{
			Code: "execution_preflight_denied", Preflight: &denied.Preflight,
		})
		return true
	}
	if errors.Is(err, pro_interfaces.ErrExecutionPreflightReviewTokenExpired) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		user := helpers.GetFromContext(r, "user").(*db.User)
		fresh, previewErr := taskPool(r).PreviewTaskExecution(taskObj, user, project.ID)
		if previewErr == nil {
			c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
				pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonExecutionPreflightStale,
				pro_interfaces.ExecutionPreflightTask, taskObj.TemplateID, &fresh, nil)
			helpers.WriteJSON(w, http.StatusConflict, pro_interfaces.ExecutionPreflightStale{
				Code: "execution_preflight_expired", Preflight: &fresh,
			})
			return true
		}
	}
	if errors.Is(err, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid) ||
		errors.Is(err, pro_interfaces.ErrExecutionPreflightReviewScopeMismatch) {
		if planned.Fingerprint != "" {
			c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
				pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonExecutionPreflightDenied,
				pro_interfaces.ExecutionPreflightTask, taskObj.TemplateID, &planned, nil)
		}
		helpers.WriteErrorStatus(w, "EXECUTION_PREFLIGHT_INVALID", http.StatusConflict)
		return true
	}
	return false
}

func (c *TaskController) recordExecutionPreflightAudit(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
	intent pro_interfaces.ExecutionPreflightIntent,
	resourceID int,
	plan *pro_interfaces.ExecutionPreflightPlan,
	changes []pro_interfaces.ExecutionPreflightChangeCode,
) {
	if c.audit == nil || plan == nil {
		return
	}
	user := helpers.UserFromContext(r)
	project := helpers.GetFromContext(r, "project").(db.Project)
	if user == nil || user.ID <= 0 || project.ID <= 0 {
		return
	}
	correlationID := helpers.CorrelationID(r.Context())
	if correlationID == "" {
		correlationID = "internal"
	}
	provenance := pro_interfaces.NewExecutionPreflightAuditProvenance(*plan, changes)
	event := pro_interfaces.NewExecutionPreflightAuditEvent(
		user.ID, project.ID, correlationID, r.RemoteAddr, r.UserAgent(),
		action, outcome, reason, intent, resourceID, provenance,
	)
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to record execution preflight audit event")
	}
}

// refillVisibleWorkflowTaskPage preserves keyset pagination semantics after
// hidden workflow-owned tasks are removed. It keeps fetching raw sentinel
// pages until it has pageSize+1 visible tasks or the underlying query ends.
func (c *TaskController) refillVisibleWorkflowTaskPage(
	r *http.Request,
	project db.Project,
	tpl any,
	params db.RetrieveQueryParams,
	pageSize int,
	first []db.TaskWithTpl,
) []db.TaskWithTpl {
	visible := c.filterWorkflowTasksForRead(r, first)
	raw := first
	for len(visible) <= pageSize && len(raw) == params.Count && len(raw) > 0 {
		next := params
		next.BeforeID = raw[len(raw)-1].ID
		var err error
		if tpl != nil {
			template := tpl.(db.Template)
			raw, err = c.store.GetTemplateTasks(template.ProjectID, template.ID, next)
		} else {
			raw, err = c.store.GetProjectTasks(project.ID, next)
		}
		if err != nil {
			break
		}
		visible = append(visible, c.filterWorkflowTasksForRead(r, raw)...)
	}
	return visible
}

// WorkflowTaskAccessMiddleware closes the direct task/log route bypass for
// workflow-owned tasks. Normal tasks retain their existing authorization.
func (c *TaskController) WorkflowTaskAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := helpers.GetFromContext(r, "task").(db.Task)
		permission := pro_interfaces.PermissionViewWorkflow
		if r.Method == http.MethodDelete {
			permission = pro_interfaces.PermissionAdministerWorkflow
		}
		if !c.authorizeWorkflowTask(r, task, permission) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WorkflowTaskControlAccessMiddleware maps direct task controls to workflow
// permissions so a task runner cannot stop or administer a restricted run by
// addressing the underlying task endpoint.
func (c *TaskController) WorkflowTaskControlAccessMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		task := helpers.GetFromContext(r, "task").(db.Task)
		permission := pro_interfaces.PermissionStopWorkflow
		switch {
		case strings.HasSuffix(r.URL.Path, "/confirm"):
			permission = pro_interfaces.PermissionStartWorkflow
		case strings.HasSuffix(r.URL.Path, "/retry-recovery"):
			permission = pro_interfaces.PermissionAdministerWorkflow
		}
		if !c.authorizeWorkflowTask(r, task, permission) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (c *TaskController) filterWorkflowTasksForRead(r *http.Request, tasks []db.TaskWithTpl) []db.TaskWithTpl {
	visible := make([]db.TaskWithTpl, 0, len(tasks))
	for _, task := range tasks {
		if c.authorizeWorkflowTask(r, task.Task, pro_interfaces.PermissionViewWorkflow) {
			visible = append(visible, task)
		}
	}
	return visible
}

func (c *TaskController) authorizeWorkflowTask(
	r *http.Request,
	task db.Task,
	permission pro_interfaces.PermissionID,
) bool {
	if task.WorkflowRunID == nil {
		if permission == pro_interfaces.PermissionAdministerWorkflow {
			user := helpers.UserFromContext(r)
			permissions, ok := helpers.GetOkFromContext(r, "permissions")
			value, valid := permissions.(db.ProjectUserPermission)
			return user != nil && (user.Admin || ok && valid && value.Can(db.CanManageProjectResources))
		}
		if permission != pro_interfaces.PermissionViewWorkflow {
			user := helpers.UserFromContext(r)
			permissions, ok := helpers.GetOkFromContext(r, "permissions")
			value, valid := permissions.(db.ProjectUserPermission)
			return user != nil && (user.Admin || ok && valid && value.Can(db.CanRunProjectTasks))
		}
		return true
	}
	if c.workflowStore == nil {
		return false
	}
	run, err := c.workflowStore.GetWorkflowRunByID(task.ProjectID, *task.WorkflowRunID)
	if err != nil {
		return false
	}
	viewAllowed, allowed, err := AuthorizeWorkflowRequest(r, run.DefinitionSnapshot, permission)
	return err == nil && viewAllowed && allowed
}

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

// GetTaskReadPermissionMiddleware narrows only execution-preview disclosure.
// A user may retain run-only permission for legacy starts, but a value-free
// preview includes labels and execution shape that are visible only to a
// template reader.
func (c *TaskController) GetTaskReadPermissionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		user := helpers.GetFromContext(r, "user").(*db.User)
		task := helpers.GetFromContext(r, "task").(db.Task)
		permissionContext, err := c.store.GetTemplatePermissionContext(project.ID, task.TemplateID, user.ID)
		if err != nil || !permissionContext.EffectivePermissions.Can(db.CanReadTemplate) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
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
