package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const workflowArtifactRetentionBodyLimit int64 = 16 * 1024

type workflowArtifactRetentionController struct {
	service pro_interfaces.WorkflowArtifactRetentionGovernanceServiceFacade
	audit   pro_interfaces.AuditServiceFacade
}

var _ pro_interfaces.WorkflowArtifactRetentionController = (*workflowArtifactRetentionController)(nil)
var _ pro_interfaces.WorkflowArtifactRetentionAuditConfigurer = (*workflowArtifactRetentionController)(nil)

func NewWorkflowArtifactRetentionController(service pro_interfaces.WorkflowArtifactRetentionGovernanceServiceFacade) pro_interfaces.WorkflowArtifactRetentionController {
	return &workflowArtifactRetentionController{service: service}
}

func (controller *workflowArtifactRetentionController) ConfigureWorkflowArtifactRetentionAudit(audit pro_interfaces.AuditServiceFacade) {
	controller.audit = audit
}

func (controller *workflowArtifactRetentionController) GetGlobalWorkflowArtifactRetention(w http.ResponseWriter, r *http.Request) {
	controller.get(w, r, db.WorkflowArtifactRetentionGlobal, nil)
}

func (controller *workflowArtifactRetentionController) PublishGlobalWorkflowArtifactRetention(w http.ResponseWriter, r *http.Request) {
	controller.publish(w, r, db.WorkflowArtifactRetentionGlobal, nil)
}

func (controller *workflowArtifactRetentionController) GetProjectWorkflowArtifactRetention(w http.ResponseWriter, r *http.Request) {
	projectID, ok := workflowArtifactRetentionProjectID(w, r)
	if !ok {
		return
	}
	controller.get(w, r, db.WorkflowArtifactRetentionProject, &projectID)
}

func (controller *workflowArtifactRetentionController) PublishProjectWorkflowArtifactRetention(w http.ResponseWriter, r *http.Request) {
	projectID, ok := workflowArtifactRetentionProjectID(w, r)
	if !ok {
		return
	}
	controller.publish(w, r, db.WorkflowArtifactRetentionProject, &projectID)
}

func (controller *workflowArtifactRetentionController) get(w http.ResponseWriter, r *http.Request, scope db.WorkflowArtifactRetentionScope, projectID *int) {
	if controller.service == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	state, err := controller.service.GetWorkflowArtifactRetention(r.Context(), scope, projectID)
	if err != nil {
		writeWorkflowArtifactRetentionError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, state)
}

func (controller *workflowArtifactRetentionController) publish(w http.ResponseWriter, r *http.Request, scope db.WorkflowArtifactRetentionScope, projectID *int) {
	user := helpers.UserFromContext(r)
	if user == nil || user.ID < 1 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var update pro_interfaces.WorkflowArtifactRetentionUpdate
	if !decodeWorkflowArtifactRetentionJSON(w, r, &update) {
		controller.recordAudit(r, projectID, user.ID, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	if update.Validate() != nil {
		controller.recordAudit(r, projectID, user.ID, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if controller.service == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	state, err := controller.service.PublishWorkflowArtifactRetention(r.Context(), scope, projectID, update, user.ID)
	if err != nil {
		controller.recordAudit(r, projectID, user.ID, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		writeWorkflowArtifactRetentionError(w, err)
		return
	}
	controller.recordAudit(r, projectID, user.ID, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonWorkflowArtifactRetentionUpdated)
	helpers.WriteJSON(w, http.StatusOK, state)
}

func (controller *workflowArtifactRetentionController) recordAudit(r *http.Request, projectID *int, actorID int, outcome pro_interfaces.AuditOutcome, reason string) {
	if controller.audit == nil || r == nil || actorID < 1 {
		return
	}
	correlationID := helpers.CorrelationID(r.Context())
	if correlationID == "" {
		correlationID = "internal"
	}
	targetID := "global"
	if projectID != nil {
		targetID = "project:" + strconv.Itoa(*projectID)
	}
	event := pro_interfaces.AuditEvent{
		CorrelationID: correlationID, ActorID: &actorID, ProjectID: projectID,
		Action: pro_interfaces.AuditActionWorkflowArtifactRetentionUpdate, TargetType: pro_interfaces.AuditTargetWorkflowArtifactRetention,
		TargetID: targetID, Outcome: outcome, Source: pro_interfaces.AuditSourceAPI, Reason: reason,
	}
	if err := controller.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("failed to record workflow artifact retention audit event")
	}
}

func workflowArtifactRetentionProjectID(w http.ResponseWriter, r *http.Request) (int, bool) {
	project, ok := helpers.GetFromContext(r, "project").(db.Project)
	if !ok || project.ID < 1 {
		w.WriteHeader(http.StatusNotFound)
		return 0, false
	}
	return project.ID, true
}

func decodeWorkflowArtifactRetentionJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	reader := http.MaxBytesReader(w, r.Body, workflowArtifactRetentionBodyLimit)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return false
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func writeWorkflowArtifactRetentionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrInvalidOperation):
		w.WriteHeader(http.StatusBadRequest)
	case errors.Is(err, pro_interfaces.ErrWorkflowArtifactRetentionConflict):
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, db.ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	default:
		helpers.WriteError(w, err)
	}
}
