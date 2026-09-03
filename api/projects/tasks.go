package projects

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

// maxTasksPageSize limits how many tasks can be requested in a single page.
const maxTasksPageSize = 200

// taskStartBodyLimit matches the bounded workflow-run transport and prevents
// a deployment-window override envelope from making task-start parsing
// unbounded. Existing task fields keep their established JSON contract.
const taskStartBodyLimit int64 = 256 * 1024

const (
	defaultTaskSummaryPageSize     = 50
	maxTaskSummaryPageSize         = 200
	taskPreflightFingerprintHeader = "X-Semaphore-Preflight-Fingerprint"
	taskPreflightTokenHeader       = "X-Semaphore-Preflight-Token"
)

type TaskController struct {
	store           db.Store
	ansibleTaskRepo db.AnsibleTaskRepository
	workflowStore   db.WorkflowManager
	audit           pro_interfaces.AuditServiceFacade
}

var _ pro_interfaces.ExecutionPreflightAuditConfigurer = (*TaskController)(nil)

func NewTaskController(store db.Store, ansibleTaskRepo db.AnsibleTaskRepository, workflowStores ...db.WorkflowManager) *TaskController {
	var workflowStore db.WorkflowManager
	if len(workflowStores) > 0 {
		workflowStore = workflowStores[0]
	}
	return &TaskController{
		store:           store,
		ansibleTaskRepo: ansibleTaskRepo,
		workflowStore:   workflowStore,
	}
}

func taskPool(r *http.Request) *tasks.TaskPool {
	return helpers.GetFromContext(r, "task_pool").(*tasks.TaskPool)
}

// ConfigureExecutionPreflightAudit attaches the optional value-free audit
// recorder after route construction. Community route wiring may omit it.
func (c *TaskController) ConfigureExecutionPreflightAudit(audit pro_interfaces.AuditServiceFacade) {
	c.audit = audit
}

// AddTask inserts a task into the database and returns a header or returns error
func (c *TaskController) AddTask(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	user := helpers.GetFromContext(r, "user").(*db.User)
	taskObj := helpers.GetFromContext(r, "task").(db.Task)

	tpl, err := c.store.GetTemplate(project.ID, taskObj.TemplateID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	newTask, planned, err := taskPool(r).AddTaskWithExecutionPreflightPlanAndDeploymentWindowOverride(
		taskObj,
		user,
		project.ID,
		tpl.App.NeedTaskAlias(),
		pro_interfaces.ExecutionPreflightReview{
			Fingerprint: r.Header.Get(taskPreflightFingerprintHeader),
			ReviewToken: r.Header.Get(taskPreflightTokenHeader),
		},
		deploymentWindowOverrideInput(r),
	)
	if c.writeTaskExecutionPreflightError(w, r, taskObj, planned, err) {
		return
	}

	if errors.Is(err, db.ErrExecutorImageCapabilityUnavailable) {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusForbidden)
		return
	}
	if errors.Is(err, db.ErrExecutorImageIncompatible) {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusConflict)
		return
	}
	if errors.Is(err, db.ErrExecutorImageInvalid) {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err != nil {
		log.WithFields(log.Fields{
			"context":     "AddTask",
			"project_id":  project.ID,
			"template_id": taskObj.TemplateID,
			"user_id":     user.ID,
		}).WithError(err).Error("Cannot add task")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if r.Header.Get(taskPreflightFingerprintHeader) != "" {
		c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
			pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonExecutionPreflightStarted,
			pro_interfaces.ExecutionPreflightTask, taskObj.TemplateID, &planned, nil)
	}

	helpers.WriteJSON(w, http.StatusCreated, newTask)
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
	var blocked *pro_interfaces.DeploymentWindowBlockedError
	if errors.As(err, &blocked) {
		helpers.WriteJSON(w, http.StatusConflict, pro_interfaces.DeploymentWindowPublicDecision{
			State: pro_interfaces.DeploymentWindowDecisionBlocked, Reason: blocked.Reason,
			NextEligibleAt: blocked.NextEligibleAt, NextEligibleKnown: blocked.NextEligibleKnown,
		})
		return true
	}
	if errors.Is(err, pro_interfaces.ErrDeploymentWindowOverrideForbidden) {
		helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_OVERRIDE_FORBIDDEN", http.StatusForbidden)
		return true
	}
	if errors.Is(err, pro_interfaces.ErrDeploymentWindowOverrideInvalid) {
		helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_INVALID_INPUT", http.StatusBadRequest)
		return true
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

// writeTasksList retrieves the tasks list (project-wide or per-template
// depending on the request context) applying the given query params and writes
// the result as JSON.
//
// When pageSize > 0 it implements keyset pagination: params.Count is expected to
// already request one extra row (pageSize + 1) so that the presence of a next
// page can be detected without an expensive COUNT(*). The extra row is trimmed
// off and whether it existed is reported via the X-Has-Next header. No total
// count is computed — that would not scale to projects with millions of tasks.
func (c *TaskController) writeTasksList(w http.ResponseWriter, r *http.Request, params db.RetrieveQueryParams, pageSize int) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	tpl := helpers.GetFromContext(r, "template")

	var err error
	var taskList []db.TaskWithTpl

	if tpl != nil {
		template := tpl.(db.Template)
		taskList, err = c.store.GetTemplateTasks(template.ProjectID, template.ID, params)
	} else {
		taskList, err = c.store.GetProjectTasks(project.ID, params)
	}

	if err != nil {
		util.LogErrorF(err, log.Fields{"error": "Bad request. Cannot get tasks list from database"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if pageSize > 0 {
		taskList = c.refillVisibleWorkflowTaskPage(r, project, tpl, params, pageSize, taskList)
	} else {
		taskList = c.filterWorkflowTasksForRead(r, taskList)
	}

	if pageSize > 0 {
		hasNext := len(taskList) > pageSize
		if hasNext {
			taskList = taskList[:pageSize]
		}
		w.Header().Set("X-Has-Next", strconv.FormatBool(hasNext))
	}

	helpers.WriteJSON(w, http.StatusOK, taskList)
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

// GetAllTasks returns all tasks for the current project
func (c *TaskController) GetAllTasks(w http.ResponseWriter, r *http.Request) {
	params := helpers.QueryParams(r.URL)
	params.Count = 1000
	c.writeTasksList(w, r, params, 0)
}

// parseTasksPageParams fills keyset pagination fields into the given base params
// from the request query and returns the effective page size. It supports the
// `count` parameter (with backward compatibility for the legacy `limit`) and the
// `before` cursor (a task id; only older tasks are returned). The page size is
// capped at maxTasksPageSize. params.Count is set to pageSize + 1 so the caller
// can detect whether a next page exists.
func parseTasksPageParams(query url.Values, base db.RetrieveQueryParams) (db.RetrieveQueryParams, int) {
	pageSize := maxTasksPageSize

	if str := query.Get("count"); str != "" {
		if v, err := strconv.Atoi(str); err == nil && v > 0 {
			pageSize = v
		}
	} else if str := query.Get("limit"); str != "" {
		if v, err := strconv.Atoi(str); err == nil && v > 0 {
			pageSize = v
		}
	}

	if pageSize > maxTasksPageSize {
		pageSize = maxTasksPageSize
	}

	// Fetch one extra row to detect the presence of a next page.
	base.Count = pageSize + 1

	if str := query.Get("before"); str != "" {
		if v, err := strconv.Atoi(str); err == nil && v > 0 {
			base.BeforeID = v
		}
	}

	return base, pageSize
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

// GetLastTasks returns a page of the most recent tasks using keyset pagination.
// The page size is controlled by the `count` query parameter (legacy `limit` is
// still accepted) and the `before` cursor selects the next, older page. The
// X-Has-Next response header reports whether more (older) tasks are available.
func (c *TaskController) GetLastTasks(w http.ResponseWriter, r *http.Request) {
	params, pageSize := parseTasksPageParams(r.URL.Query(), helpers.QueryParams(r.URL))
	c.writeTasksList(w, r, params, pageSize)
}

// GetTask returns a task based on its id
func (c *TaskController) GetTask(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	helpers.WriteJSON(w, http.StatusOK, task)
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

func (c *TaskController) GetTaskPermissionsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		user := helpers.GetFromContext(r, "user").(*db.User)
		task := helpers.GetFromContext(r, "task").(db.Task)

		permissions := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)

		permissionContext, err := c.store.GetTemplatePermissionContext(
			project.ID, task.TemplateID, user.ID,
		)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !permissionContext.EffectivePermissions.Can(db.CanRunTemplate) {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		permissions = applyEffectiveTemplatePermissions(
			permissions,
			db.TemplatePermissionsToProject(permissionContext.EffectivePermissions),
		)

		r = helpers.SetContextValue(r, "permissions", permissions)
		next.ServeHTTP(w, r)
	})
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

// GetTaskMiddleware is middleware that gets a task by id and sets the context to it or panics
func (c *TaskController) GetTaskMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		taskID, ok := helpers.GetIntParamOrAbort("task_id", w, r)

		if !ok {
			return
		}

		task, err := c.store.GetTask(project.ID, taskID)
		if err != nil {
			util.LogErrorF(err, log.Fields{"error": "Cannot get task from database"})
			helpers.WriteError(w, err)
			return
		}

		r = helpers.SetContextValue(r, "task", task)
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

// NewTaskMiddleware is middleware that binds a task from the request body and sets the context to it
func (c *TaskController) NewTaskMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		var payload struct {
			db.Task
			DeploymentWindowOverride *pro_interfaces.DeploymentWindowOverrideInput `json:"deployment_window_override,omitempty"`
		}

		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, taskStartBodyLimit))
		if err := decoder.Decode(&payload); err != nil {
			helpers.WriteErrorStatus(w, "TASK_START_INVALID_INPUT", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			helpers.WriteErrorStatus(w, "TASK_START_INVALID_INPUT", http.StatusBadRequest)
			return
		}

		r = helpers.SetContextValue(r, "task", payload.Task)
		r = helpers.SetContextValue(r, "deployment_window_override", payload.DeploymentWindowOverride)
		next.ServeHTTP(w, r)
	})
}

func deploymentWindowOverrideInput(r *http.Request) *pro_interfaces.DeploymentWindowOverrideInput {
	value, _ := helpers.GetOkFromContext(r, "deployment_window_override")
	override, _ := value.(*pro_interfaces.DeploymentWindowOverrideInput)
	return override
}

func (c *TaskController) GetAnsibleTaskHosts(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)
	hosts, err := c.ansibleTaskRepo.GetAnsibleTaskHosts(project.ID, task.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, hosts)
}

func (c *TaskController) GetAnsibleTaskErrors(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)
	hosts, err := c.ansibleTaskRepo.GetAnsibleTaskErrors(project.ID, task.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, hosts)
}

// GetTaskStages returns the logged task stages by id and writes it as json or returns error
func (c *TaskController) GetTaskStages(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	stages, err := c.store.GetTaskStages(project.ID, task.ID)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	for i := range stages {
		if stages[i].JSON == "" {
			continue
		}
		var res any
		err = json.Unmarshal([]byte(stages[i].JSON), &res)
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		stages[i].Result = res
	}

	helpers.WriteJSON(w, http.StatusOK, stages)
}

// GetTaskOutput returns the logged task output by id and writes it as json or returns error
func (c *TaskController) GetTaskOutput(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	var output []db.TaskOutput
	output, err := c.store.GetTaskOutputs(project.ID, task.ID, db.RetrieveQueryParams{})

	if err != nil {
		util.LogErrorF(err, log.Fields{"error": "Bad request. Cannot get task output from database"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, output)
}

func outputToBytes(lines []db.TaskOutput) []byte {
	var buffer bytes.Buffer
	for _, line := range lines {
		output := util.ClearFromAnsiCodes(line.Output)
		buffer.WriteString(output)
		buffer.WriteByte('\n')
	}
	return buffer.Bytes()
}

func (c *TaskController) GetTaskRawOutput(w http.ResponseWriter, r *http.Request) {
	task := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	const chunkSize = 10000
	offset := 0

	eof := false
	for !eof {
		var output []db.TaskOutput
		output, err := c.store.GetTaskOutputs(project.ID, task.ID, db.RetrieveQueryParams{Offset: offset, Count: chunkSize})

		if err != nil {
			if offset == 0 {
				util.LogErrorF(err, log.Fields{"error": "Bad request. Cannot get task output from database"})
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			util.LogErrorF(err, log.Fields{"error": "Cannot get task output from database"})
			return
		}

		if offset == 0 {
			w.Header().Set("content-type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
		}

		readSize := len(output)

		if readSize > 0 {
			offset += readSize
			data := outputToBytes(output)
			if _, err := w.Write(data); err != nil {
				return
			}
		}

		eof = readSize < chunkSize
	}
}

func (c *TaskController) ConfirmTask(w http.ResponseWriter, r *http.Request) {
	targetTask := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	if targetTask.ProjectID != project.ID {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err := taskPool(r).ConfirmTask(targetTask)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *TaskController) RejectTask(w http.ResponseWriter, r *http.Request) {
	targetTask := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	if targetTask.ProjectID != project.ID {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	err := taskPool(r).RejectTask(targetTask)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *TaskController) StopTask(w http.ResponseWriter, r *http.Request) {
	targetTask := helpers.GetFromContext(r, "task").(db.Task)
	project := helpers.GetFromContext(r, "project").(db.Project)

	if targetTask.ProjectID != project.ID {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var stopObj struct {
		Force bool `json:"force"`
	}

	if !helpers.Bind(w, r, &stopObj) {
		return
	}

	err := taskPool(r).StopTask(targetTask, stopObj.Force)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// RemoveTask removes a task from the database
func (c *TaskController) RemoveTask(w http.ResponseWriter, r *http.Request) {
	targetTask := helpers.GetFromContext(r, "task").(db.Task)
	editor := helpers.GetFromContext(r, "user").(*db.User)
	project := helpers.GetFromContext(r, "project").(db.Project)

	activeTask, err := taskPool(r).GetTask(targetTask.ID)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	if activeTask != nil {
		// can't delete task in queue or running
		// task must be stopped firstly
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !editor.Admin {
		log.Warn(editor.Username + " is not permitted to delete task logs")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	err = c.store.DeleteTaskWithOutputs(project.ID, targetTask.ID)
	if err != nil {
		util.LogErrorF(err, log.Fields{"error": "Bad request. Cannot delete task from database"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *TaskController) GetTaskStats(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)

	var tplID *int
	if tpl := helpers.GetFromContext(r, "template"); tpl != nil {
		id := tpl.(db.Template).ID
		tplID = &id
	}

	filter := db.TaskFilter{}

	if start := r.URL.Query().Get("start"); start != "" {
		d, err := time.Parse("2006-01-02", start)
		if err != nil {
			helpers.WriteErrorStatus(w, "Invalid start date", http.StatusBadRequest)
			return
		}
		filter.Start = &d
	}

	if end := r.URL.Query().Get("end"); end != "" {
		d, err := time.Parse("2006-01-02", end)
		if err != nil {
			helpers.WriteErrorStatus(w, "Invalid end date", http.StatusBadRequest)
			return
		}
		filter.End = &d
	}

	if userId := r.URL.Query().Get("user_id"); userId != "" {
		u, err := strconv.Atoi(userId)
		if err != nil {
			helpers.WriteErrorStatus(w, "Invalid user_id", http.StatusBadRequest)
			return
		}
		filter.UserID = &u
	}

	stats, err := c.store.GetTaskStats(project.ID, tplID, db.TaskStatUnitDay, filter)
	if err != nil {
		util.LogErrorF(err, log.Fields{"error": "Bad request. Cannot get task stats from database"})
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, stats)
}

func (c *TaskController) StopAllTasks(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	tpl := helpers.GetFromContext(r, "template").(db.Template)

	var stopObj struct {
		Force bool `json:"force"`
	}

	// optional body; ignore bind error and default Force=false
	if ok := helpers.Bind(w, r, &stopObj); !ok {
		helpers.WriteErrorStatus(w, "Not allowed", http.StatusBadRequest)
		return
	}

	taskPool(r).StopTasksByTemplate(project.ID, tpl.ID, stopObj.Force)
	w.WriteHeader(http.StatusNoContent)
}
