// Package projects provides the clean-room enhanced contract implementation.
package projects

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	community "github.com/semaphoreui/semaphore/community-pro/api/projects"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

type ProjectRunnerControllerImpl struct {
	runnerService      server.RunnerService
	capabilityProvider pro_interfaces.CapabilityProvider
	audit              pro_interfaces.AuditServiceFacade
}

var (
	NewTerraformInventoryController = community.NewTerraformInventoryController
	NewWorkflowController           = community.NewWorkflowController
)

func NewProjectRunnerController(
	_ pro_interfaces.SubscriptionService,
	runnerService server.RunnerService,
	capabilityProvider pro_interfaces.CapabilityProvider,
	audit pro_interfaces.AuditServiceFacade,
) pro_interfaces.ProjectRunnerController {
	return &ProjectRunnerControllerImpl{
		runnerService:      runnerService,
		capabilityProvider: capabilityProvider,
		audit:              audit,
	}
}

type runnerRegistrationResponse struct {
	db.Runner
	RegistrationToken string `json:"registration_token"`
}

func (c *ProjectRunnerControllerImpl) GetRunners(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead,
		pro_interfaces.AuditActionProjectRunnerList, projectTarget(project.ID)) {
		return
	}
	runners, err := helpers.Store(r).GetRunners(project.ID, false, db.RunnerFilterIgnoreTags, nil)
	if err != nil {
		c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerList, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError, projectTarget(project.ID))
		helpers.WriteErrorStatus(w, "PROJECT_RUNNERS_UNAVAILABLE", http.StatusInternalServerError)
		return
	}
	now := tz.Now()
	offlineTimeout := util.Config.RunnersOfflineTimeout()
	for index := range runners {
		runners[index].Registered = runners[index].IsRegistered()
		runners[index].FillStatus(now, offlineTimeout)
	}
	c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerList, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), projectTarget(project.ID))
	helpers.WriteJSON(w, http.StatusOK, runners)
}

func (c *ProjectRunnerControllerImpl) AddRunner(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite,
		pro_interfaces.AuditActionProjectRunnerCreate, projectTarget(project.ID)) {
		return
	}
	var runner db.Runner
	if !helpers.Bind(w, r, &runner) {
		c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerCreate, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput, projectTarget(project.ID))
		return
	}
	runner.Name = strings.TrimSpace(runner.Name)
	if runner.Name == "" {
		c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerCreate, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput, projectTarget(project.ID))
		helpers.WriteErrorStatus(w, "PROJECT_RUNNER_NAME_REQUIRED", http.StatusBadRequest)
		return
	}
	runner.ProjectID = &project.ID
	created, token, err := c.runnerService.CreateProjectRunner(runner)
	if err != nil {
		c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerCreate, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError, projectTarget(project.ID))
		helpers.WriteErrorStatus(w, "PROJECT_RUNNER_CREATE_FAILED", http.StatusBadRequest)
		return
	}
	created.Registered = false
	created.FillStatus(tz.Now(), util.Config.RunnersOfflineTimeout())
	c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerCreate, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), runnerTarget(created.ID))
	helpers.WriteJSON(w, http.StatusCreated, runnerRegistrationResponse{
		Runner:            created,
		RegistrationToken: token,
	})
}

func (c *ProjectRunnerControllerImpl) RunnerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		runnerID, err := helpers.GetIntParam("runner_id", w, r)
		if err != nil {
			return
		}
		runner, err := helpers.Store(r).GetRunner(project.ID, runnerID)
		if err != nil {
			c.recordAudit(r, actionForRunnerMethod(r.Method), pro_interfaces.AuditOutcomeDenied, string(pro_interfaces.CapabilityReasonInsufficientPermission), runnerTarget(runnerID))
			helpers.WriteErrorStatus(w, "PROJECT_RUNNER_NOT_FOUND", http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, helpers.SetContextValue(r, "runner", &runner))
	})
}

func (c *ProjectRunnerControllerImpl) GetRunner(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead,
		pro_interfaces.AuditActionProjectRunnerRead, runnerTarget(runner.ID)) {
		return
	}
	runner.Registered = runner.IsRegistered()
	runner.FillStatus(tz.Now(), util.Config.RunnersOfflineTimeout())
	c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerRead, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), runnerTarget(runner.ID))
	helpers.WriteJSON(w, http.StatusOK, runner)
}

func (c *ProjectRunnerControllerImpl) RegenerateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite,
		pro_interfaces.AuditActionProjectRunnerIssue, runnerTarget(runner.ID)) {
		return
	}
	token, err := c.runnerService.RegenerateRegistrationToken(*runner)
	if err != nil {
		c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerIssue, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError, runnerTarget(runner.ID))
		helpers.WriteErrorStatus(w, "PROJECT_RUNNER_REGISTRATION_FAILED", http.StatusBadRequest)
		return
	}
	c.recordAudit(r, pro_interfaces.AuditActionProjectRunnerIssue, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), runnerTarget(runner.ID))
	helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"registration_token": token,
		"runner_id":          runner.ID,
	})
}

func (c *ProjectRunnerControllerImpl) GetRunnerTags(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead,
		pro_interfaces.AuditActionProjectRunnerList, projectTarget(project.ID)) {
		return
	}
	tags, err := helpers.Store(r).GetRunnerTags(project.ID)
	if err != nil {
		helpers.WriteErrorStatus(w, "PROJECT_RUNNER_TAGS_UNAVAILABLE", http.StatusInternalServerError)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, tags)
}

func (*ProjectRunnerControllerImpl) UpdateRunner(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*ProjectRunnerControllerImpl) DeleteRunner(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*ProjectRunnerControllerImpl) SetRunnerActive(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (*ProjectRunnerControllerImpl) ClearRunnerCache(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *ProjectRunnerControllerImpl) requireCapability(
	w http.ResponseWriter,
	r *http.Request,
	access pro_interfaces.CapabilityAccess,
	action pro_interfaces.AuditAction,
	targetID string,
) bool {
	if c.capabilityProvider == nil {
		c.recordAudit(r, action, pro_interfaces.AuditOutcomeDenied,
			string(pro_interfaces.CapabilityReasonProviderUnavailable), targetID)
		helpers.WriteErrorStatus(w, "PROJECT_RUNNERS_UNAVAILABLE", http.StatusNotFound)
		return false
	}
	user := helpers.GetFromContext(r, "user").(*db.User)
	snapshot, err := c.capabilityProvider.Resolve(r.Context(), pro_interfaces.CapabilityRequest{
		UserID:  user.ID,
		IsAdmin: user.Admin,
		At:      tz.Now(),
	})
	if err != nil {
		c.recordAudit(r, action, pro_interfaces.AuditOutcomeFailure,
			pro_interfaces.AuditReasonOperationError, targetID)
		helpers.WriteErrorStatus(w, "PROJECT_RUNNERS_UNAVAILABLE", http.StatusServiceUnavailable)
		return false
	}
	if err := snapshot.Require(pro_interfaces.CapabilityProjectRunners, access); err != nil {
		decision := snapshot.Decision(pro_interfaces.CapabilityProjectRunners)
		status := http.StatusForbidden
		if decision.Reason() == pro_interfaces.CapabilityReasonProviderUnavailable {
			status = http.StatusNotFound
		}
		c.recordAudit(r, action, pro_interfaces.AuditOutcomeDenied, string(decision.Reason()), targetID)
		helpers.WriteErrorStatus(w, "PROJECT_RUNNERS_UNAVAILABLE", status)
		return false
	}
	return true
}

func (c *ProjectRunnerControllerImpl) recordAudit(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
	targetID string,
) {
	if c.audit == nil {
		return
	}
	user := helpers.GetFromContext(r, "user").(*db.User)
	project := helpers.GetFromContext(r, "project").(db.Project)
	actorID := user.ID
	projectID := project.ID
	correlationID := helpers.CorrelationID(r.Context())
	if correlationID == "" {
		correlationID = "internal"
	}
	event := pro_interfaces.AuditEvent{
		CorrelationID: correlationID,
		ActorID:       &actorID,
		ProjectID:     &projectID,
		Action:        action,
		TargetType:    pro_interfaces.AuditTargetProjectRunner,
		TargetID:      targetID,
		Outcome:       outcome,
		Source:        pro_interfaces.AuditSourceAPI,
		Reason:        reason,
	}
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store project runner audit event")
	}
}

func projectTarget(projectID int) string { return fmt.Sprintf("project:%d", projectID) }

func runnerTarget(runnerID int) string { return fmt.Sprintf("runner:%d", runnerID) }

func actionForRunnerMethod(method string) pro_interfaces.AuditAction {
	if method == http.MethodGet || method == http.MethodHead {
		return pro_interfaces.AuditActionProjectRunnerRead
	}
	return pro_interfaces.AuditActionProjectRunnerIssue
}

var _ pro_interfaces.ProjectRunnerController = (*ProjectRunnerControllerImpl)(nil)
