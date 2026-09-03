package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const (
	policyGuardrailSourceBodyLimit int64 = int64(pro_interfaces.MaxPolicyGuardrailYAMLBytes + 16*1024)
	policyGuardrailImpactBodyLimit int64 = 2 * 1024 * 1024
	// A fixture contains one bounded source document plus a complete bounded
	// execution input (not just the small draft envelope), so it shares the
	// impact request ceiling while source itself remains capped separately.
	policyGuardrailFixtureBodyLimit = policyGuardrailImpactBodyLimit
	defaultPolicyGuardrailPageSize  = 25
	maxPolicyGuardrailImpactInputs  = 100
)

// policyGuardrailController is transport-only. Scope comes exclusively from
// ProjectMiddleware: an installed project is project scope, its absence is the
// global endpoint. Request bodies cannot select a tenant or actor.
type policyGuardrailController struct {
	service pro_interfaces.PolicyGuardrailGovernanceServiceFacade
	audit   pro_interfaces.AuditServiceFacade
}

var _ pro_interfaces.PolicyGuardrailController = (*policyGuardrailController)(nil)
var _ pro_interfaces.PolicyGuardrailAuditConfigurer = (*policyGuardrailController)(nil)

func NewPolicyGuardrailController(service pro_interfaces.PolicyGuardrailGovernanceServiceFacade) pro_interfaces.PolicyGuardrailController {
	return &policyGuardrailController{service: service}
}

func (c *policyGuardrailController) ConfigurePolicyGuardrailAudit(audit pro_interfaces.AuditServiceFacade) {
	c.audit = audit
}

type policyGuardrailDraftInput struct {
	SourceYAML       string `json:"source_yaml"`
	ExpectedRevision int    `json:"expected_revision"`
}

type policyGuardrailSourceInput struct {
	SourceYAML string `json:"source_yaml"`
}

func (c *policyGuardrailController) Get(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok || !c.available(w) {
		return
	}
	state, err := c.service.Get(r.Context(), scope, projectID)
	if err != nil {
		writePolicyGuardrailError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, state)
}

func (c *policyGuardrailController) SaveDraft(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok {
		return
	}
	var input policyGuardrailDraftInput
	if !decodePolicyGuardrailJSON(w, r, policyGuardrailSourceBodyLimit, &input) || !validPolicyGuardrailRevision(input.ExpectedRevision) || len(input.SourceYAML) > pro_interfaces.MaxPolicyGuardrailYAMLBytes {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailDraftSave, 0, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput)
		if !validPolicyGuardrailRevision(input.ExpectedRevision) || len(input.SourceYAML) > pro_interfaces.MaxPolicyGuardrailYAMLBytes {
			policyGuardrailBadRequest(w)
		}
		return
	}
	actor, ok := policyGuardrailActor(w, r)
	if !ok || !c.available(w) {
		return
	}
	draft, err := c.service.SaveDraft(r.Context(), scope, projectID, input.SourceYAML, input.ExpectedRevision, actor)
	if err != nil {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailDraftSave, input.ExpectedRevision, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		writePolicyGuardrailError(w, err)
		return
	}
	if !validPolicyGuardrailRevision(draft.Revision) {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailDraftSave, input.ExpectedRevision, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		policyGuardrailInternalError(w)
		return
	}
	c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailDraftSave, draft.Revision, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonPolicyGuardrailDraftSaved)
	helpers.WriteJSON(w, http.StatusOK, draft)
}

func (c *policyGuardrailController) Validate(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok {
		return
	}
	var input policyGuardrailSourceInput
	if !decodePolicyGuardrailJSON(w, r, policyGuardrailSourceBodyLimit, &input) || len(input.SourceYAML) > pro_interfaces.MaxPolicyGuardrailYAMLBytes {
		if len(input.SourceYAML) > pro_interfaces.MaxPolicyGuardrailYAMLBytes {
			policyGuardrailBadRequest(w)
		}
		return
	}
	if !c.available(w) {
		return
	}
	helpers.WriteJSON(w, http.StatusOK, c.service.Validate(r.Context(), scope, projectID, input.SourceYAML))
}

func (c *policyGuardrailController) TestFixture(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok {
		return
	}
	var input pro_interfaces.PolicyGuardrailFixtureRequest
	if !decodePolicyGuardrailJSON(w, r, policyGuardrailFixtureBodyLimit, &input) ||
		len(input.SourceYAML) > pro_interfaces.MaxPolicyGuardrailYAMLBytes || !validPolicyGuardrailInputScope(scope, projectID, input.Input) {
		if len(input.SourceYAML) > pro_interfaces.MaxPolicyGuardrailYAMLBytes || !validPolicyGuardrailInputScope(scope, projectID, input.Input) {
			policyGuardrailBadRequest(w)
		}
		return
	}
	if !c.available(w) {
		return
	}
	evaluation, err := c.service.TestFixture(r.Context(), scope, projectID, input)
	if err != nil {
		writePolicyGuardrailError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, evaluation)
}

func (c *policyGuardrailController) Diff(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok || !c.available(w) {
		return
	}
	from, to, ok := policyGuardrailDiffQuery(w, r)
	if !ok {
		return
	}
	diff, err := c.service.Diff(r.Context(), scope, projectID, from, to)
	if err != nil {
		writePolicyGuardrailError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, diff)
}

func (c *policyGuardrailController) Publish(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok {
		return
	}
	var input pro_interfaces.PolicyGuardrailPublishRequest
	if !decodePolicyGuardrailJSON(w, r, policyGuardrailSourceBodyLimit, &input) || !validPolicyGuardrailRevision(input.ExpectedDraftRevision) {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailPublish, 0, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput)
		if !validPolicyGuardrailRevision(input.ExpectedDraftRevision) {
			policyGuardrailBadRequest(w)
		}
		return
	}
	actor, ok := policyGuardrailActor(w, r)
	if !ok || !c.available(w) {
		return
	}
	revision, err := c.service.Publish(r.Context(), scope, projectID, input, actor)
	if err != nil {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailPublish, input.ExpectedDraftRevision, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		writePolicyGuardrailError(w, err)
		return
	}
	if !validPolicyGuardrailRevision(revision.Revision) {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailPublish, input.ExpectedDraftRevision, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		policyGuardrailInternalError(w)
		return
	}
	c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailPublish, revision.Revision, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonPolicyGuardrailPublished)
	helpers.WriteJSON(w, http.StatusCreated, revision)
}

func (c *policyGuardrailController) Impact(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok {
		return
	}
	var input pro_interfaces.PolicyGuardrailImpactRequest
	if !decodePolicyGuardrailJSON(w, r, policyGuardrailImpactBodyLimit, &input) || !validPolicyGuardrailImpactScope(scope, projectID, input) {
		if !validPolicyGuardrailImpactScope(scope, projectID, input) {
			policyGuardrailBadRequest(w)
		}
		return
	}
	if !c.available(w) {
		return
	}
	result, err := c.service.Impact(r.Context(), scope, projectID, input)
	if err != nil {
		writePolicyGuardrailError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *policyGuardrailController) Revisions(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok || !c.available(w) {
		return
	}
	params, ok := policyGuardrailHistoryParams(w, r)
	if !ok {
		return
	}
	values, err := c.service.Revisions(r.Context(), scope, projectID, params)
	if err != nil {
		writePolicyGuardrailError(w, err)
		return
	}
	if values == nil {
		values = []db.PolicyGuardrailRevision{}
	}
	helpers.WriteJSON(w, http.StatusOK, values)
}

func (c *policyGuardrailController) Evaluations(w http.ResponseWriter, r *http.Request) {
	_, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok || !c.available(w) {
		return
	}
	params, ok := policyGuardrailHistoryParams(w, r)
	if !ok {
		return
	}
	values, err := c.service.Evaluations(r.Context(), projectID, params)
	if err != nil {
		writePolicyGuardrailError(w, err)
		return
	}
	if values == nil {
		values = []db.PolicyGuardrailEvaluationRecord{}
	}
	helpers.WriteJSON(w, http.StatusOK, values)
}

func (c *policyGuardrailController) Rollback(w http.ResponseWriter, r *http.Request) {
	scope, projectID, ok := policyGuardrailRequestScope(w, r)
	if !ok {
		return
	}
	var input pro_interfaces.PolicyGuardrailRollbackRequest
	if !decodePolicyGuardrailJSON(w, r, policyGuardrailSourceBodyLimit, &input) || !validPolicyGuardrailRollback(input) {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailRollback, input.Revision, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput)
		if !validPolicyGuardrailRollback(input) {
			policyGuardrailBadRequest(w)
		}
		return
	}
	actor, ok := policyGuardrailActor(w, r)
	if !ok || !c.available(w) {
		return
	}
	revision, err := c.service.Rollback(r.Context(), scope, projectID, input, actor)
	if err != nil {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailRollback, input.Revision, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		writePolicyGuardrailError(w, err)
		return
	}
	if !validPolicyGuardrailRevision(revision.Revision) {
		c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailRollback, input.Revision, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		policyGuardrailInternalError(w)
		return
	}
	c.recordAudit(r, scope, projectID, pro_interfaces.AuditActionPolicyGuardrailRollback, revision.Revision, pro_interfaces.AuditOutcomeAllowed, pro_interfaces.AuditReasonPolicyGuardrailRolledBack)
	helpers.WriteJSON(w, http.StatusCreated, revision)
}

func (c *policyGuardrailController) available(w http.ResponseWriter) bool {
	if c == nil || c.service == nil {
		helpers.WriteErrorStatus(w, "POLICY_GUARDRAILS_UNAVAILABLE", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (c *policyGuardrailController) recordAudit(
	r *http.Request,
	scope pro_interfaces.PolicyGuardrailScope,
	projectID *int,
	action pro_interfaces.AuditAction,
	revision int,
	outcome pro_interfaces.AuditOutcome,
	reason string,
) {
	if c == nil || c.audit == nil || r == nil {
		return
	}
	user := helpers.UserFromContext(r)
	if user == nil || user.ID <= 0 {
		return
	}
	if revision < 0 || revision > pro_interfaces.AuditPolicyGuardrailRevisionMax {
		revision = 0
	}
	correlationID := helpers.CorrelationID(r.Context())
	if correlationID == "" {
		correlationID = "internal"
	}
	targetID := "global"
	if scope == pro_interfaces.PolicyGuardrailScopeProject && projectID != nil && *projectID > 0 {
		targetID = "project:" + strconv.Itoa(*projectID)
	}
	event := pro_interfaces.AuditEvent{
		CorrelationID: correlationID,
		ActorID:       &user.ID,
		ProjectID:     projectID,
		Action:        action,
		TargetType:    pro_interfaces.AuditTargetPolicyGuardrail,
		TargetID:      targetID,
		Outcome:       outcome,
		Source:        pro_interfaces.AuditSourceAPI,
		Reason:        reason,
		PolicyGuardrailProvenance: &pro_interfaces.AuditPolicyGuardrailProvenance{
			Scope: scope, Revision: revision,
		},
	}
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to record policy guardrail governance audit event")
	}
}

func policyGuardrailRequestScope(w http.ResponseWriter, r *http.Request) (pro_interfaces.PolicyGuardrailScope, *int, bool) {
	if r == nil {
		policyGuardrailBadRequest(w)
		return "", nil, false
	}
	project, present := helpers.GetFromContext(r, "project").(db.Project)
	if !present {
		return pro_interfaces.PolicyGuardrailScopeGlobal, nil, true
	}
	if project.ID <= 0 {
		policyGuardrailBadRequest(w)
		return "", nil, false
	}
	projectID := project.ID
	return pro_interfaces.PolicyGuardrailScopeProject, &projectID, true
}

func policyGuardrailActor(w http.ResponseWriter, r *http.Request) (int, bool) {
	user := helpers.UserFromContext(r)
	if user == nil || user.ID <= 0 {
		w.WriteHeader(http.StatusForbidden)
		return 0, false
	}
	return user.ID, true
}

func decodePolicyGuardrailJSON(w http.ResponseWriter, r *http.Request, limit int64, destination any) bool {
	if r == nil || r.Body == nil {
		policyGuardrailBadRequest(w)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			policyGuardrailTooLarge(w)
		} else {
			policyGuardrailBadRequest(w)
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		policyGuardrailBadRequest(w)
		return false
	}
	return true
}

func validPolicyGuardrailInputScope(scope pro_interfaces.PolicyGuardrailScope, projectID *int, input pro_interfaces.PolicyGuardrailEvaluationInput) bool {
	if input.Validate() != nil {
		return false
	}
	return scope == pro_interfaces.PolicyGuardrailScopeGlobal || projectID != nil && input.ProjectID == *projectID
}

func validPolicyGuardrailImpactScope(scope pro_interfaces.PolicyGuardrailScope, projectID *int, request pro_interfaces.PolicyGuardrailImpactRequest) bool {
	if len(request.Inputs) == 0 || len(request.Inputs) > maxPolicyGuardrailImpactInputs {
		return false
	}
	for _, input := range request.Inputs {
		if !validPolicyGuardrailInputScope(scope, projectID, input) {
			return false
		}
	}
	return true
}

func validPolicyGuardrailRollback(input pro_interfaces.PolicyGuardrailRollbackRequest) bool {
	return validPolicyGuardrailRevision(input.Revision) && validPolicyGuardrailRevision(input.ExpectedDraftRevision) && input.Reason == strings.TrimSpace(input.Reason) &&
		input.Reason != "" && len(input.Reason) <= db.MaxPolicyGuardrailRollbackReasonBytes && !strings.ContainsAny(input.Reason, "\x00\r\n")
}

func validPolicyGuardrailRevision(value int) bool {
	return value > 0 && value <= pro_interfaces.AuditPolicyGuardrailRevisionMax
}

func policyGuardrailDiffQuery(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	query := r.URL.Query()
	if len(query) != 2 || len(query["from_revision"]) != 1 || len(query["to_revision"]) != 1 {
		policyGuardrailBadRequest(w)
		return 0, 0, false
	}
	from, fromErr := strconv.Atoi(strings.TrimSpace(query.Get("from_revision")))
	to, toErr := strconv.Atoi(strings.TrimSpace(query.Get("to_revision")))
	if fromErr != nil || toErr != nil || !validPolicyGuardrailRevision(from) || !validPolicyGuardrailRevision(to) || from == to {
		policyGuardrailBadRequest(w)
		return 0, 0, false
	}
	return from, to, true
}

func policyGuardrailHistoryParams(w http.ResponseWriter, r *http.Request) (db.RetrieveQueryParams, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "count" && key != "before") || len(values) != 1 {
			policyGuardrailBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
	}
	params := db.RetrieveQueryParams{Count: defaultPolicyGuardrailPageSize}
	if raw, exists := query["count"]; exists {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 || value > db.MaxPolicyGuardrailHistoryPage {
			policyGuardrailBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
		params.Count = value
	}
	if raw, exists := query["before"]; exists {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 {
			policyGuardrailBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
		params.BeforeID = value
	}
	return params, true
}

func policyGuardrailBadRequest(w http.ResponseWriter) {
	helpers.WriteErrorStatus(w, "POLICY_GUARDRAIL_INVALID_INPUT", http.StatusBadRequest)
}

func policyGuardrailTooLarge(w http.ResponseWriter) {
	helpers.WriteErrorStatus(w, "POLICY_GUARDRAIL_REQUEST_TOO_LARGE", http.StatusRequestEntityTooLarge)
}

func policyGuardrailInternalError(w http.ResponseWriter) {
	helpers.WriteErrorStatus(w, "POLICY_GUARDRAIL_OPERATION_FAILED", http.StatusInternalServerError)
}

func writePolicyGuardrailError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrPolicyGuardrailDraftRevisionConflict), errors.Is(err, db.ErrPolicyGuardrailPublishConflict):
		helpers.WriteErrorStatus(w, "POLICY_GUARDRAIL_REVISION_CONFLICT", http.StatusConflict)
	case errors.Is(err, db.ErrNotFound), errors.Is(err, db.ErrPolicyGuardrailTenantMismatch):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, db.ErrInvalidOperation):
		policyGuardrailBadRequest(w)
	default:
		policyGuardrailInternalError(w)
	}
}
