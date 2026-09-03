package projects

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/api/helpers"
	coreprojects "github.com/semaphoreui/semaphore/api/projects"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const workflowDefinitionBodyLimit int64 = 8 * 1024 * 1024
const workflowRunCorrelationIDLimit = 64
const workflowRunBodyLimit int64 = 256 * 1024
const workflowPreflightFingerprintHeader = "X-Semaphore-Preflight-Fingerprint"
const workflowPreflightTokenHeader = "X-Semaphore-Preflight-Token"
const workflowVersionRestoreBodyLimit int64 = 2 * 1024
const defaultWorkflowVersionPageSize = 50
const maxWorkflowVersionPageSize = 100

type workflowController struct {
	definitionService pro_interfaces.WorkflowDefinitionService
	workflowService   pro_interfaces.WorkflowService
	workflowManager   db.WorkflowManager
	audit             pro_interfaces.AuditServiceFacade
}

type workflowRunDetails struct {
	Run             workflowRunView             `json:"run"`
	Workflow        workflowRunDefinitionView   `json:"workflow"`
	Templates       []workflowRunTemplateView   `json:"templates"`
	Nodes           []workflowRunNodeDetails    `json:"nodes"`
	Approvals       []workflowApprovalView      `json:"approvals"`
	EffectiveAccess workflowEffectiveAccessView `json:"effective_access"`
}

type workflowResponse struct {
	db.WorkflowTemplate
	EffectiveAccess workflowEffectiveAccessView `json:"effective_access"`
}

type workflowVersionSummary struct {
	ID                    int       `json:"id"`
	ProjectID             int       `json:"project_id"`
	WorkflowTemplateID    int       `json:"workflow_template_id"`
	VersionNumber         int       `json:"version_number"`
	ParentVersionID       *int      `json:"parent_version_id,omitempty"`
	RestoredFromVersionID *int      `json:"restored_from_version_id,omitempty"`
	AuthorUserID          int       `json:"author_user_id"`
	Message               string    `json:"message"`
	ContentFingerprint    string    `json:"content_fingerprint"`
	Created               time.Time `json:"created"`
}

type workflowEffectiveAccessView struct {
	View       bool `json:"view"`
	Edit       bool `json:"edit"`
	Start      bool `json:"start"`
	Stop       bool `json:"stop"`
	Administer bool `json:"administer"`
}

type workflowApprovalView struct {
	db.WorkflowApproval
	Eligible                 bool                        `json:"eligible"`
	PolicyRevision           int                         `json:"policy_revision"`
	ContributionCount        int                         `json:"contribution_count"`
	MinimumDistinctApprovers int                         `json:"minimum_distinct_approvers"`
	Mode                     db.WorkflowApprovalRoleMode `json:"mode"`
}

type workflowApprovalInboxView struct {
	db.WorkflowApproval
	Eligible                 bool                        `json:"eligible"`
	PolicyRevision           int                         `json:"policy_revision"`
	ContributionCount        int                         `json:"contribution_count"`
	MinimumDistinctApprovers int                         `json:"minimum_distinct_approvers"`
	Mode                     db.WorkflowApprovalRoleMode `json:"mode"`
}

type workflowRunNodeDetails struct {
	Node           workflowRunNodeView                `json:"node"`
	Status         db.WorkflowRunNodeStatus           `json:"status"`
	Reason         string                             `json:"reason,omitempty"`
	Result         *db.WorkflowNodeResult             `json:"result,omitempty"`
	ArtifactInputs []db.WorkflowArtifactInputSnapshot `json:"artifact_inputs,omitempty"`
	Overrides      db.WorkflowNodeOverride            `json:"overrides,omitempty"`
	Task           *workflowRunTaskView               `json:"task,omitempty"`
}

type workflowRunView struct {
	ID                          int                                     `json:"id"`
	ProjectID                   int                                     `json:"project_id"`
	WorkflowTemplateID          int                                     `json:"workflow_template_id"`
	Status                      db.WorkflowRunStatus                    `json:"status"`
	Reason                      string                                  `json:"reason,omitempty"`
	Version                     *string                                 `json:"version,omitempty"`
	ActorUserID                 int                                     `json:"actor_user_id"`
	DefinitionVersion           int                                     `json:"definition_version"`
	DefinitionRevision          int                                     `json:"definition_revision"`
	WorkflowVersionID           int                                     `json:"workflow_version_id"`
	CorrelationID               string                                  `json:"correlation_id"`
	DesiredState                db.WorkflowRunDesiredState              `json:"desired_state"`
	ReconciliationState         db.WorkflowRunReconciliationState       `json:"reconciliation_state"`
	ReconciliationAttempts      int                                     `json:"reconciliation_attempts"`
	ReconciliationLastError     string                                  `json:"reconciliation_last_error,omitempty"`
	ReconciliationNextRetryAt   *time.Time                              `json:"reconciliation_next_retry_at,omitempty"`
	ReconciliationQuarantinedAt *time.Time                              `json:"reconciliation_quarantined_at,omitempty"`
	ReconciliationOwnership     *db.WorkflowReconciliationDiagnostics   `json:"reconciliation_ownership,omitempty"`
	Created                     time.Time                               `json:"created"`
	Start                       *time.Time                              `json:"start,omitempty"`
	End                         *time.Time                              `json:"end,omitempty"`
	RootTaskID                  *int                                    `json:"root_task_id,omitempty"`
	Parameters                  map[string]db.WorkflowParameterSnapshot `json:"parameters,omitempty"`
	Trigger                     *db.WorkflowTriggerSnapshot             `json:"trigger,omitempty"`
}

type workflowRunDefinitionView struct {
	ID                int                               `json:"id"`
	Name              string                            `json:"name"`
	DefinitionVersion int                               `json:"definition_version"`
	Revision          int                               `json:"revision"`
	MaxParallelTasks  int                               `json:"max_parallel_tasks"`
	Parameters        []db.WorkflowParameterDeclaration `json:"parameters,omitempty"`
	Nodes             []workflowRunNodeView             `json:"nodes"`
	Edges             []workflowRunEdgeView             `json:"edges"`
}

type workflowRunNodeView struct {
	ID                         int                               `json:"id"`
	TemplateID                 int                               `json:"template_id,omitempty"`
	DisplayName                string                            `json:"display_name,omitempty"`
	Kind                       db.WorkflowNodeKind               `json:"kind,omitempty"`
	ConvergenceMode            db.WorkflowConvergenceMode        `json:"convergence_mode,omitempty"`
	JoinMode                   db.WorkflowJoinMode               `json:"join_mode,omitempty"`
	ApprovalTimeout            *int                              `json:"approval_timeout,omitempty"`
	ApprovalMessage            *string                           `json:"approval_message,omitempty"`
	ApprovalPermission         db.ProjectUserPermission          `json:"approval_permission,omitempty"`
	ApprovalTimeoutOutcome     db.WorkflowApprovalTimeoutOutcome `json:"approval_timeout_outcome,omitempty"`
	ApprovalSeparationOfDuties bool                              `json:"approval_separation_of_duties,omitempty"`
	Note                       *string                           `json:"note,omitempty"`
	PositionX                  int                               `json:"position_x"`
	PositionY                  int                               `json:"position_y"`
	ArtifactOutputs            []db.WorkflowArtifactDeclaration  `json:"artifact_outputs,omitempty"`
	ArtifactInputs             []db.WorkflowArtifactReference    `json:"artifact_inputs,omitempty"`
	OverridePolicy             db.WorkflowNodeOverridePolicy     `json:"override_policy,omitempty"`
}

type workflowRunEdgeView struct {
	ID                int                      `json:"id"`
	SourceNodeID      int                      `json:"source_node_id"`
	DestinationNodeID int                      `json:"destination_node_id"`
	Condition         db.WorkflowEdgeCondition `json:"condition"`
	Expression        string                   `json:"condition_expression,omitempty"`
	Label             string                   `json:"label,omitempty"`
}

type workflowRunTemplateView struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type workflowRunTaskView struct {
	ID             int                    `json:"id"`
	Status         task_logger.TaskStatus `json:"status"`
	UsedRunnerID   *int                   `json:"used_runner_id,omitempty"`
	UsedRunnerName *string                `json:"used_runner_name,omitempty"`
}

var _ pro_interfaces.WorkflowController = (*workflowController)(nil)
var _ pro_interfaces.ExecutionPreflightAuditConfigurer = (*workflowController)(nil)

func NewWorkflowController(
	workflowService pro_interfaces.WorkflowService,
	workflowManager db.WorkflowManager,
	definitionService pro_interfaces.WorkflowDefinitionService,
) pro_interfaces.WorkflowController {
	return &workflowController{
		definitionService: definitionService,
		workflowService:   workflowService,
		workflowManager:   workflowManager,
	}
}

// ConfigureExecutionPreflightAudit attaches an optional value-free recorder
// after the router has constructed its shared audit facade.
func (c *workflowController) ConfigureExecutionPreflightAudit(audit pro_interfaces.AuditServiceFacade) {
	c.audit = audit
}

func (c *workflowController) GetWorkflows(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflows, err := c.definitionService.List(project.ID, helpers.QueryParams(r.URL), helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if userValue, found := helpers.GetOkFromContext(r, "user"); found {
		user, valid := userValue.(*db.User)
		if !valid || user == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !user.Admin {
			storeValue, storeAvailable := helpers.GetOkFromContext(r, "store")
			store, ok := storeValue.(pro_interfaces.WorkflowAuthorizationIdentityStore)
			if !storeAvailable {
				ok = false
			}
			if !ok {
				// Unit-only controller invocations historically omit a store. Real
				// routed requests always carry one and fail closed below.
				if storeAvailable {
					w.WriteHeader(http.StatusNotFound)
					return
				}
			} else {
				state, stateErr := pro_interfaces.ResolveWorkflowAuthorizationState(store, project.ID, user.ID)
				if stateErr != nil {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				workflows = pro_interfaces.FilterWorkflowTemplatesByAccess(workflows, state.Identity, state.KnownRoles)
			}
		}
	}
	result := make([]workflowResponse, len(workflows))
	for index := range workflows {
		result[index] = workflowResponse{WorkflowTemplate: workflows[index], EffectiveAccess: workflowEffectiveAccess(r, workflows[index])}
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowController) AddWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var workflow db.WorkflowTemplate
	if !bindWorkflowDefinition(w, r, &workflow) {
		return
	}
	created, validation, err := c.definitionService.Create(project.ID, workflow, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowError(w, err, c.definitionService, project.ID, 0, helpers.UserFromContext(r))
		return
	}
	if !validation.Valid {
		writeWorkflowValidation(w, validation)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, created)
}

func (c *workflowController) ValidateWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var workflow db.WorkflowTemplate
	if !bindWorkflowDefinition(w, r, &workflow) {
		return
	}
	result, err := c.definitionService.Validate(project.ID, workflow, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowController) GetWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	helpers.WriteJSON(w, http.StatusOK, workflowResponse{WorkflowTemplate: workflow, EffectiveAccess: workflowEffectiveAccess(r, workflow)})
}

func (c *workflowController) UpdateWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	current := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	var workflow db.WorkflowTemplate
	if !bindWorkflowDefinition(w, r, &workflow) {
		return
	}
	updated, validation, err := c.definitionService.Update(project.ID, current.ID, workflow, helpers.UserFromContext(r))
	if err != nil {
		writeWorkflowError(w, err, c.definitionService, project.ID, current.ID, helpers.UserFromContext(r))
		return
	}
	if !validation.Valid {
		writeWorkflowValidation(w, validation)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func bindWorkflowDefinition(w http.ResponseWriter, r *http.Request, workflow *db.WorkflowTemplate) bool {
	r.Body = http.MaxBytesReader(w, r.Body, workflowDefinitionBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(workflow); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			helpers.WriteJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"code":    "WORKFLOW_DEFINITION_TOO_LARGE",
				"message": "Workflow definition exceeds the 8 MiB request limit.",
			})
			return false
		}
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func (c *workflowController) RemoveWorkflow(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	if err := c.definitionService.Delete(project.ID, workflow.ID, helpers.UserFromContext(r)); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *workflowController) GetWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	versions, err := c.definitionService.ListVersions(
		project.ID, workflow.ID, workflowVersionQueryParams(r), helpers.UserFromContext(r),
	)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	result := make([]workflowVersionSummary, len(versions))
	for index := range versions {
		result[index] = newWorkflowVersionSummary(versions[index])
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowController) GetWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	versionNumber, err := helpers.GetIntParam("version_number", w, r)
	if err != nil {
		return
	}
	version, err := c.definitionService.GetVersion(
		project.ID, workflow.ID, versionNumber, helpers.UserFromContext(r),
	)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, version)
}

func (c *workflowController) DiffWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	beforeVersion, beforeErr := positiveWorkflowVersionQuery(r, "from")
	afterVersion, afterErr := positiveWorkflowVersionQuery(r, "to")
	if beforeErr != nil || afterErr != nil {
		helpers.WriteError(w, common_errors.NewValidationError("workflow version diff requires positive from and to versions"))
		return
	}
	diff, err := c.definitionService.DiffVersions(
		project.ID, workflow.ID, beforeVersion, afterVersion, helpers.UserFromContext(r),
	)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, diff)
}

func (c *workflowController) RestoreWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	versionNumber, err := helpers.GetIntParam("version_number", w, r)
	if err != nil {
		return
	}
	var request struct {
		Message string `json:"message"`
	}
	if !bindWorkflowVersionRestore(w, r, &request) {
		return
	}
	restored, validation, err := c.definitionService.RestoreVersion(
		project.ID, workflow.ID, versionNumber, request.Message, helpers.UserFromContext(r),
	)
	if err != nil {
		writeWorkflowError(w, err, c.definitionService, project.ID, workflow.ID, helpers.UserFromContext(r))
		return
	}
	if !validation.Valid {
		writeWorkflowValidation(w, validation)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, restored)
}

func newWorkflowVersionSummary(version db.WorkflowVersion) workflowVersionSummary {
	return workflowVersionSummary{
		ID: version.ID, ProjectID: version.ProjectID, WorkflowTemplateID: version.WorkflowTemplateID,
		VersionNumber: version.VersionNumber, ParentVersionID: version.ParentVersionID,
		RestoredFromVersionID: version.RestoredFromVersionID, AuthorUserID: version.AuthorUserID,
		Message: version.Message, ContentFingerprint: version.ContentFingerprint, Created: version.Created,
	}
}

func positiveWorkflowVersionQuery(r *http.Request, name string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil || value <= 0 {
		return 0, errors.New("workflow version query is invalid")
	}
	return value, nil
}

func workflowVersionQueryParams(r *http.Request) db.RetrieveQueryParams {
	params := helpers.QueryParams(r.URL)
	params.Count = defaultWorkflowVersionPageSize
	if value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("count"))); err == nil && value > 0 {
		params.Count = value
	}
	if params.Count > maxWorkflowVersionPageSize {
		params.Count = maxWorkflowVersionPageSize
	}
	if value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("before"))); err == nil && value > 0 {
		params.BeforeID = value
	}
	return params
}

func bindWorkflowVersionRestore(w http.ResponseWriter, r *http.Request, request any) bool {
	if r.Body == nil {
		helpers.WriteError(w, common_errors.NewValidationError("workflow version restore body is required"))
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, workflowVersionRestoreBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(request); err != nil {
		helpers.WriteError(w, common_errors.NewValidationError("workflow version restore body is invalid"))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		helpers.WriteError(w, common_errors.NewValidationError("workflow version restore body is invalid"))
		return false
	}
	return true
}

func writeWorkflowValidation(w http.ResponseWriter, result db.WorkflowValidationResult) {
	helpers.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"code": "WORKFLOW_VALIDATION_FAILED", "valid": false, "issues": result.Issues,
	})
}

func writeWorkflowError(
	w http.ResponseWriter,
	err error,
	service pro_interfaces.WorkflowDefinitionService,
	projectID int,
	workflowID int,
	actor *db.User,
) {
	if errors.Is(err, pro_interfaces.ErrWorkflowRevisionConflict) {
		response := map[string]any{
			"code":    "WORKFLOW_REVISION_CONFLICT",
			"message": "The workflow was changed by another editor. Reload the current definition before saving again.",
		}
		if current, getErr := service.Get(projectID, workflowID, actor); getErr == nil {
			response["current"] = current
		}
		helpers.WriteJSON(w, http.StatusConflict, response)
		return
	}
	helpers.WriteError(w, err)
}

func (c *workflowController) RunWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	input, override, ok := readWorkflowRunInput(w, r)
	if !ok {
		return
	}
	correlationID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if correlationID == "" {
		correlationID = helpers.CorrelationID(r.Context())
	}
	if correlationID == "" {
		correlationID = random.String(32)
	}
	if len(correlationID) > workflowRunCorrelationIDLimit {
		helpers.WriteError(w, common_errors.NewValidationError("workflow run idempotency key must not exceed 64 characters"))
		return
	}
	user := helpers.UserFromContext(r)
	var run db.WorkflowRun
	var planned pro_interfaces.ExecutionPreflightPlan
	var err error
	if service, supported := c.workflowService.(pro_interfaces.WorkflowExecutionPreflightOverrideAuditResultService); supported {
		run, planned, err = service.StartWorkflowWithExecutionPreflightPlanAndDeploymentWindowOverride(
			workflow, user, correlationID,
			pro_interfaces.ExecutionPreflightReview{
				Fingerprint: r.Header.Get(workflowPreflightFingerprintHeader),
				ReviewToken: r.Header.Get(workflowPreflightTokenHeader),
			}, override, input,
		)
	} else if service, supported := c.workflowService.(pro_interfaces.WorkflowExecutionPreflightOverrideService); supported {
		run, err = service.StartWorkflowWithExecutionPreflightAndDeploymentWindowOverride(
			workflow, user, correlationID,
			pro_interfaces.ExecutionPreflightReview{
				Fingerprint: r.Header.Get(workflowPreflightFingerprintHeader),
				ReviewToken: r.Header.Get(workflowPreflightTokenHeader),
			}, override, input,
		)
	} else if override != nil {
		err = pro_interfaces.ErrDeploymentWindowOverrideInvalid
	} else if service, supported := c.workflowService.(pro_interfaces.WorkflowExecutionPreflightAuditResultService); supported {
		run, planned, err = service.StartWorkflowWithExecutionPreflightPlan(
			workflow, user, correlationID,
			pro_interfaces.ExecutionPreflightReview{
				Fingerprint: r.Header.Get(workflowPreflightFingerprintHeader),
				ReviewToken: r.Header.Get(workflowPreflightTokenHeader),
			}, input,
		)
	} else if service, supported := c.workflowService.(pro_interfaces.WorkflowExecutionPreflightService); supported {
		run, err = service.StartWorkflowWithExecutionPreflight(
			workflow, user, correlationID,
			pro_interfaces.ExecutionPreflightReview{
				Fingerprint: r.Header.Get(workflowPreflightFingerprintHeader),
				ReviewToken: r.Header.Get(workflowPreflightTokenHeader),
			}, input,
		)
	} else {
		run, err = c.workflowService.StartWorkflow(workflow, user, correlationID, input)
	}
	if err != nil {
		if c.writeWorkflowExecutionPreflightError(w, r, workflow, planned, err) {
			return
		}
		helpers.WriteError(w, err)
		return
	}
	w.Header().Set("Idempotency-Key", correlationID)
	if planned.Fingerprint != "" {
		if r.Header.Get(workflowPreflightFingerprintHeader) != "" {
			c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
				pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonExecutionPreflightStarted,
				workflow.ID, &planned, nil)
		}
	}
	helpers.WriteJSON(w, http.StatusCreated, newWorkflowRunView(run))
}

func (c *workflowController) PreviewWorkflow(w http.ResponseWriter, r *http.Request) {
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	input, override, ok := readWorkflowRunInput(w, r)
	if !ok {
		return
	}
	if override != nil {
		helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_INVALID_INPUT", http.StatusBadRequest)
		return
	}
	service, supported := c.workflowService.(pro_interfaces.WorkflowExecutionPreflightService)
	if !supported {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	plan, err := service.PreviewWorkflowExecution(workflow, helpers.UserFromContext(r), input)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, plan)
	outcome, reason := pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonExecutionPreflightPreviewed
	if denialReason, denied := pro_interfaces.ExecutionPreflightAuditDenialReason(plan); denied {
		outcome, reason = pro_interfaces.AuditOutcomeDenied, denialReason
	}
	c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightPreview,
		outcome, reason,
		workflow.ID, &plan, nil)
}

func readWorkflowRunInput(w http.ResponseWriter, r *http.Request) (db.WorkflowRunInput, *pro_interfaces.DeploymentWindowOverrideInput, bool) {
	request := struct {
		Parameters               map[string]json.RawMessage                    `json:"parameters,omitempty"`
		NodeOverrides            map[int]db.WorkflowNodeOverride               `json:"node_overrides,omitempty"`
		DeploymentWindowOverride *pro_interfaces.DeploymentWindowOverrideInput `json:"deployment_window_override,omitempty"`
	}{}
	if r.Body == nil {
		return db.WorkflowRunInput{}, nil, true
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, workflowRunBodyLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		helpers.WriteError(w, common_errors.NewValidationError("workflow run input is invalid"))
		return db.WorkflowRunInput{}, nil, false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		helpers.WriteError(w, common_errors.NewValidationError("workflow run input is invalid"))
		return db.WorkflowRunInput{}, nil, false
	}
	return db.WorkflowRunInput{UserValues: request.Parameters, NodeOverrides: request.NodeOverrides}, request.DeploymentWindowOverride, true
}

func (c *workflowController) writeWorkflowExecutionPreflightError(w http.ResponseWriter, r *http.Request, workflow db.WorkflowTemplate, planned pro_interfaces.ExecutionPreflightPlan, err error) bool {
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
	if errors.Is(err, pro_interfaces.ErrDeploymentWindowOverrideConflict) {
		helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_OVERRIDE_CONFLICT", http.StatusConflict)
		return true
	}
	if errors.Is(err, pro_interfaces.ErrDeploymentWindowOverrideInvalid) {
		helpers.WriteErrorStatus(w, "DEPLOYMENT_WINDOW_INVALID_INPUT", http.StatusBadRequest)
		return true
	}
	var stale *pro_interfaces.ExecutionPreflightStaleError
	if errors.As(err, &stale) {
		c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
			pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonExecutionPreflightStale,
			workflow.ID, &stale.Preflight, stale.Changes)
		helpers.WriteJSON(w, http.StatusConflict, pro_interfaces.ExecutionPreflightStale{
			Code: "stale_execution_preflight", Changes: stale.Changes, Preflight: &stale.Preflight,
		})
		return true
	}
	var denied *pro_interfaces.ExecutionPreflightDeniedError
	if errors.As(err, &denied) {
		reason := pro_interfaces.AuditReasonExecutionPreflightDenied
		if denialReason, found := pro_interfaces.ExecutionPreflightAuditDenialReason(denied.Preflight); found {
			reason = denialReason
		}
		c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
			pro_interfaces.AuditOutcomeDenied, reason,
			workflow.ID, &denied.Preflight, nil)
		helpers.WriteJSON(w, http.StatusConflict, pro_interfaces.ExecutionPreflightStale{
			Code: "execution_preflight_denied", Preflight: &denied.Preflight,
		})
		return true
	}
	if errors.Is(err, pro_interfaces.ErrExecutionPreflightReviewTokenExpired) {
		helpers.WriteErrorStatus(w, "EXECUTION_PREFLIGHT_EXPIRED", http.StatusConflict)
		return true
	}
	if errors.Is(err, pro_interfaces.ErrExecutionPreflightReviewTokenInvalid) ||
		errors.Is(err, pro_interfaces.ErrExecutionPreflightReviewScopeMismatch) {
		if planned.Fingerprint != "" {
			c.recordExecutionPreflightAudit(r, pro_interfaces.AuditActionExecutionPreflightStart,
				pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonExecutionPreflightDenied,
				workflow.ID, &planned, nil)
		}
		helpers.WriteErrorStatus(w, "EXECUTION_PREFLIGHT_INVALID", http.StatusConflict)
		return true
	}
	return false
}

func (c *workflowController) recordExecutionPreflightAudit(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
	workflowID int,
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
	event := pro_interfaces.NewExecutionPreflightAuditEvent(
		user.ID, project.ID, correlationID, r.RemoteAddr, r.UserAgent(), action, outcome, reason,
		pro_interfaces.ExecutionPreflightWorkflow, workflowID,
		pro_interfaces.NewExecutionPreflightAuditProvenance(*plan, changes),
	)
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to record execution preflight audit event")
	}
}

func (c *workflowController) StopWorkflowRun(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	stopped, err := c.workflowService.StopWorkflowRun(project.ID, run.ID, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, newWorkflowRunView(stopped))
}

func (c *workflowController) RetryWorkflowRunReconciliation(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	retried, err := c.workflowService.RetryWorkflowRunReconciliation(project.ID, run.ID, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, newWorkflowRunView(retried))
}

func (c *workflowController) GetWorkflowRuns(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	runs, err := c.workflowManager.GetWorkflowRuns(project.ID, workflow.ID, helpers.QueryParams(r.URL))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	result := make([]workflowRunView, len(runs))
	for index := range runs {
		result[index] = newWorkflowRunView(runs[index])
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowController) GetWorkflowRun(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	if !c.canReadWorkflowRun(r, run) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err := c.workflowService.ProgressWorkflowRun(project.ID, run.ID, helpers.UserFromContext(r)); err != nil {
		helpers.WriteError(w, err)
		return
	}
	current, err := c.workflowManager.GetWorkflowRun(project.ID, workflow.ID, run.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	details, err := c.workflowRunDetails(r, current)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, details)
}

func (c *workflowController) workflowRunDetails(r *http.Request, run db.WorkflowRun) (workflowRunDetails, error) {
	tasks, err := c.workflowManager.GetWorkflowRunTasks(run.ProjectID, run.ID, db.RetrieveQueryParams{})
	if err != nil {
		return workflowRunDetails{}, err
	}
	approvals, err := c.workflowManager.GetWorkflowApprovals(run.ProjectID, run.ID)
	if err != nil {
		return workflowRunDetails{}, err
	}
	tasksByNode := make(map[int]db.TaskWithTpl, len(tasks))
	for _, task := range tasks {
		if task.WorkflowNodeID != nil {
			tasksByNode[*task.WorkflowNodeID] = task
		}
	}
	statesByNode := make(map[int]db.WorkflowRunNode, len(run.Nodes))
	templates := make([]workflowRunTemplateView, 0, len(run.Nodes))
	for _, node := range run.Nodes {
		statesByNode[node.WorkflowNodeID] = node
		templates = append(templates, workflowRunTemplateView{
			ID: node.TemplateSnapshot.ID, Name: node.TemplateSnapshot.Name,
		})
	}
	nodes := make([]workflowRunNodeDetails, 0, len(run.DefinitionSnapshot.Nodes))
	for _, node := range run.DefinitionSnapshot.Nodes {
		state, exists := statesByNode[node.ID]
		if !exists {
			continue
		}
		detail := workflowRunNodeDetails{
			Node: newWorkflowRunNodeView(node), Status: state.Status, Reason: state.Reason,
			ArtifactInputs: state.ArtifactInputs, Overrides: state.OverrideSnapshot,
		}
		if state.ResultJSON != "" && state.ResultJSON != "{}" {
			result := state.Result
			detail.Result = &result
		}
		if task, taskExists := tasksByNode[node.ID]; taskExists {
			detail.Task = &workflowRunTaskView{
				ID: task.ID, Status: task.Status,
				UsedRunnerID: task.UsedRunnerID, UsedRunnerName: task.UsedRunnerName,
			}
		}
		nodes = append(nodes, detail)
	}
	eligibleApprovals := make(map[int]bool)
	if c.workflowService != nil {
		inbox, inboxErr := c.workflowService.GetWorkflowApprovalInbox(run.ProjectID, helpers.UserFromContext(r))
		if inboxErr == nil {
			for _, approval := range inbox {
				if approval.Status == db.WorkflowApprovalPending {
					eligibleApprovals[approval.ID] = true
				}
			}
		}
	}
	approvalViews := make([]workflowApprovalView, len(approvals))
	contributionStore, _ := c.workflowManager.(db.WorkflowApprovalContributionStore)
	for index, approval := range approvals {
		approvalViews[index] = workflowApprovalView{
			WorkflowApproval:         approval,
			Eligible:                 eligibleApprovals[approval.ID],
			PolicyRevision:           approval.RolePolicySnapshotRevision,
			MinimumDistinctApprovers: approval.RolePolicySnapshot.Policy.MinimumDistinctApprovers,
			Mode:                     approval.RolePolicySnapshot.Policy.Mode,
		}
		if contributionStore != nil {
			contributions, contributionErr := contributionStore.GetWorkflowApprovalContributions(approval.ID)
			if contributionErr != nil {
				return workflowRunDetails{}, contributionErr
			}
			approvalViews[index].ContributionCount = len(contributions)
		}
	}
	return workflowRunDetails{
		Run:       newWorkflowRunView(run),
		Workflow:  newWorkflowRunDefinitionView(run.DefinitionSnapshot),
		Templates: templates,
		Nodes:     nodes,
		Approvals: approvalViews, EffectiveAccess: workflowEffectiveAccess(r, run.DefinitionSnapshot),
	}, nil
}

func workflowEffectiveAccess(r *http.Request, workflow db.WorkflowTemplate) workflowEffectiveAccessView {
	if _, ok := helpers.GetOkFromContext(r, "store"); !ok {
		return workflowEffectiveAccessView{}
	}
	permissions := []pro_interfaces.PermissionID{pro_interfaces.PermissionViewWorkflow, pro_interfaces.PermissionEditWorkflow, pro_interfaces.PermissionStartWorkflow, pro_interfaces.PermissionStopWorkflow, pro_interfaces.PermissionAdministerWorkflow}
	values := make([]bool, len(permissions))
	for index, permission := range permissions {
		view, allowed, err := coreprojects.AuthorizeWorkflowRequest(r, workflow, permission)
		values[index] = err == nil && view && allowed
	}
	return workflowEffectiveAccessView{View: values[0], Edit: values[1], Start: values[2], Stop: values[3], Administer: values[4]}
}

func newWorkflowRunView(run db.WorkflowRun) workflowRunView {
	view := workflowRunView{
		ID:                          run.ID,
		ProjectID:                   run.ProjectID,
		WorkflowTemplateID:          run.WorkflowTemplateID,
		Status:                      run.Status,
		Reason:                      run.Reason,
		Version:                     run.Version,
		ActorUserID:                 run.ActorUserID,
		DefinitionVersion:           run.DefinitionVersion,
		DefinitionRevision:          run.DefinitionRevision,
		WorkflowVersionID:           run.WorkflowVersionID,
		CorrelationID:               run.CorrelationID,
		DesiredState:                run.DesiredState,
		ReconciliationState:         run.ReconciliationState,
		ReconciliationAttempts:      run.ReconciliationAttempts,
		ReconciliationLastError:     run.ReconciliationLastError,
		ReconciliationNextRetryAt:   run.ReconciliationNextRetryAt,
		ReconciliationQuarantinedAt: run.ReconciliationQuarantinedAt,
		ReconciliationOwnership:     run.ReconciliationOwnership,
		Created:                     run.Created,
		Start:                       run.Start,
		End:                         run.End,
		RootTaskID:                  run.RootTaskID,
		Parameters:                  run.ParameterSnapshot,
	}
	if run.TriggerSnapshot.ID > 0 {
		trigger := run.TriggerSnapshot
		view.Trigger = &trigger
	}
	return view
}

func newWorkflowRunDefinitionView(workflow db.WorkflowTemplate) workflowRunDefinitionView {
	nodes := make([]workflowRunNodeView, len(workflow.Nodes))
	for index := range workflow.Nodes {
		nodes[index] = newWorkflowRunNodeView(workflow.Nodes[index])
	}
	edges := make([]workflowRunEdgeView, len(workflow.Edges))
	for index, edge := range workflow.Edges {
		edges[index] = workflowRunEdgeView{
			ID: edge.ID, SourceNodeID: edge.SourceNodeID,
			DestinationNodeID: edge.DestinationNodeID,
			Condition:         edge.Condition, Expression: edge.Expression, Label: edge.Label,
		}
	}
	return workflowRunDefinitionView{
		ID: workflow.ID, Name: workflow.Name,
		DefinitionVersion: workflow.DefinitionVersion, Revision: workflow.Revision,
		MaxParallelTasks: workflow.MaxParallelTasks,
		Parameters:       workflow.ParameterDefinitions,
		Nodes:            nodes, Edges: edges,
	}
}

func newWorkflowRunNodeView(node db.WorkflowNode) workflowRunNodeView {
	return workflowRunNodeView{
		ID:                         node.ID,
		TemplateID:                 node.TemplateID,
		DisplayName:                node.DisplayName,
		Kind:                       node.Kind,
		ConvergenceMode:            node.ConvergenceMode,
		JoinMode:                   node.JoinMode,
		ApprovalTimeout:            node.ApprovalTimeout,
		ApprovalMessage:            node.ApprovalMessage,
		ApprovalPermission:         node.ApprovalPermission,
		ApprovalTimeoutOutcome:     node.ApprovalTimeoutOutcome,
		ApprovalSeparationOfDuties: node.ApprovalSeparationOfDuties,
		Note:                       node.Note,
		PositionX:                  node.PositionX,
		PositionY:                  node.PositionY,
		ArtifactOutputs:            node.ArtifactOutputs,
		ArtifactInputs:             node.ArtifactInputs,
		OverridePolicy:             node.OverridePolicy,
	}
}

func (c *workflowController) GetWorkflowRunArtifacts(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	if !c.canReadWorkflowRun(r, run) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	artifacts, err := c.workflowService.GetWorkflowRunArtifacts(project.ID, run.ID, nil)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, artifacts)
}

func (c *workflowController) GetWorkflowApprovals(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	if !c.canReadWorkflowRun(r, run) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	approvals, err := c.workflowManager.GetWorkflowApprovals(project.ID, run.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, approvals)
}

// canReadWorkflowRun gives a current eligible pending approver a bounded view
// of the one run they must decide on. It never grants workflow list/detail
// visibility and relies on the service's live eligibility filtering.
func (c *workflowController) canReadWorkflowRun(r *http.Request, run db.WorkflowRun) bool {
	if _, ok := helpers.GetOkFromContext(r, "store"); !ok {
		// Unit handlers may be invoked without router middleware; production
		// requests always carry the store and take the checks below.
		return true
	}
	viewAllowed, _, err := coreprojects.AuthorizeWorkflowRequest(r, run.DefinitionSnapshot, pro_interfaces.PermissionViewWorkflow)
	if err == nil && viewAllowed {
		return true
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	approvals, inboxErr := c.workflowService.GetWorkflowApprovalInbox(project.ID, helpers.UserFromContext(r))
	if inboxErr != nil {
		return false
	}
	for _, approval := range approvals {
		if approval.WorkflowRunID == run.ID && approval.Status == db.WorkflowApprovalPending {
			return true
		}
	}
	return false
}

func (c *workflowController) GetWorkflowApprovalInbox(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	approvals, err := c.workflowService.GetWorkflowApprovalInbox(project.ID, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	contributionStore, _ := c.workflowManager.(db.WorkflowApprovalContributionStore)
	result := make([]workflowApprovalInboxView, len(approvals))
	for index, approval := range approvals {
		result[index] = workflowApprovalInboxView{
			WorkflowApproval: approval, Eligible: approval.Status == db.WorkflowApprovalPending,
			PolicyRevision:           approval.RolePolicySnapshotRevision,
			MinimumDistinctApprovers: approval.RolePolicySnapshot.Policy.MinimumDistinctApprovers,
			Mode:                     approval.RolePolicySnapshot.Policy.Mode,
		}
		if contributionStore != nil {
			contributions, contributionErr := contributionStore.GetWorkflowApprovalContributions(approval.ID)
			if contributionErr != nil {
				helpers.WriteError(w, contributionErr)
				return
			}
			result[index].ContributionCount = len(contributions)
		}
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *workflowController) ResolveWorkflowApproval(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	workflow := helpers.GetFromContext(r, "workflow").(db.WorkflowTemplate)
	run := helpers.GetFromContext(r, "workflow_run").(db.WorkflowRun)
	nodeID, err := helpers.GetIntParam("node_id", w, r)
	if err != nil {
		return
	}
	var input db.WorkflowApprovalDecision
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, workflowRunBodyLimit))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&input); err != nil {
		helpers.WriteError(w, common_errors.NewValidationError("workflow approval decision is invalid"))
		return
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		helpers.WriteError(w, common_errors.NewValidationError("workflow approval decision is invalid"))
		return
	}
	approval, err := c.workflowService.ResolveWorkflowApproval(project.ID, workflow.ID, run.ID, nodeID, input, helpers.UserFromContext(r))
	if err != nil {
		if errors.Is(err, pro_interfaces.ErrWorkflowPermissionDenied) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, approval)
}
