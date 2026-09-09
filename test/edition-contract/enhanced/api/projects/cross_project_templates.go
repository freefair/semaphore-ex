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
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const crossProjectTemplateBodyLimit int64 = 8 * 1024
const defaultCrossProjectTemplatePageSize = 50

type crossProjectTemplateController struct {
	service pro_interfaces.CrossProjectTemplateService
	audit   pro_interfaces.AuditServiceFacade
}

type crossProjectTemplateGrantView struct {
	ID                int                                   `json:"id"`
	OwnerProjectID    int                                   `json:"owner_project_id"`
	ConsumerProjectID int                                   `json:"consumer_project_id"`
	TemplateID        int                                   `json:"template_id"`
	MinVersion        int                                   `json:"min_version"`
	MaxVersion        int                                   `json:"max_version"`
	Operations        db.CrossProjectTemplateGrantOperation `json:"operations"`
	Status            db.CrossProjectTemplateGrantStatus    `json:"status"`
	Revision          int                                   `json:"revision"`
	Reason            string                                `json:"reason"`
	Created           time.Time                             `json:"created"`
}

func safeCrossProjectTemplateGrant(grant db.CrossProjectTemplateGrant) crossProjectTemplateGrantView {
	return crossProjectTemplateGrantView{ID: grant.ID, OwnerProjectID: grant.OwnerProjectID, ConsumerProjectID: grant.ConsumerProjectID, TemplateID: grant.TemplateID, MinVersion: grant.MinTemplateVersion, MaxVersion: grant.MaxTemplateVersion, Operations: grant.Operations, Status: grant.Status, Revision: grant.Revision, Reason: grant.Reason, Created: grant.Created}
}

func NewCrossProjectTemplateController(service pro_interfaces.CrossProjectTemplateService) pro_interfaces.CrossProjectTemplateController {
	return &crossProjectTemplateController{service: service}
}

func bindCrossProject(w http.ResponseWriter, r *http.Request, value any, allowedKeys ...string) bool {
	if r.Body == nil {
		crossBadRequest(w)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, crossProjectTemplateBodyLimit)
	d := json.NewDecoder(r.Body)
	var fields map[string]json.RawMessage
	if err := d.Decode(&fields); err != nil || fields == nil {
		crossBadRequest(w)
		return false
	}
	if err := d.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		crossBadRequest(w)
		return false
	}
	allowed := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		allowed[key] = struct{}{}
	}
	for key := range fields {
		if _, ok := allowed[key]; !ok {
			crossBadRequest(w)
			return false
		}
	}
	payload, err := json.Marshal(fields)
	if err != nil || json.Unmarshal(payload, value) != nil {
		crossBadRequest(w)
		return false
	}
	return true
}
func crossParams(w http.ResponseWriter, r *http.Request) (db.RetrieveQueryParams, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "count" && key != "before") || len(values) != 1 {
			crossBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
	}
	params := db.RetrieveQueryParams{Count: defaultCrossProjectTemplatePageSize}
	if raw, ok := query["count"]; ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 {
			crossBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
		params.Count = min(value, db.MaxCrossProjectTemplateGrantPageSize)
	}
	if raw, ok := query["before"]; ok {
		value, err := strconv.Atoi(strings.TrimSpace(raw[0]))
		if err != nil || value <= 0 {
			crossBadRequest(w)
			return db.RetrieveQueryParams{}, false
		}
		params.BeforeID = value
	}
	return params, true
}

func crossExpectedRevision(w http.ResponseWriter, r *http.Request) (int, bool) {
	query := r.URL.Query()
	values, ok := query["expected_revision"]
	if len(query) != 1 || !ok || len(values) != 1 {
		crossBadRequest(w)
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(values[0]))
	if err != nil || value <= 0 {
		crossBadRequest(w)
		return 0, false
	}
	return value, true
}

func crossBadRequest(w http.ResponseWriter) {
	helpers.WriteErrorStatus(w, "CROSS_PROJECT_TEMPLATE_INVALID_INPUT", http.StatusBadRequest)
}

func crossWriteServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrCrossProjectTemplateGrantRevisionConflict) {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusConflict)
		return
	}
	helpers.WriteError(w, err)
}

func crossID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	value, ok := helpers.GetIntParamOrAbort(name, w, r)
	if !ok {
		return 0, false
	}
	if value <= 0 {
		crossBadRequest(w)
		return 0, false
	}
	return value, true
}
func crossProject(r *http.Request) db.Project {
	return helpers.GetFromContext(r, "project").(db.Project)
}

func (c *crossProjectTemplateController) PublishTemplateVersion(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "template_id")
	if !ok {
		return
	}
	v, reused, err := c.service.PublishTemplateVersion(p.ID, id, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if c.audit != nil {
		actor := helpers.UserFromContext(r)
		projectID := p.ID
		_ = c.audit.Record(r.Context(), pro_interfaces.AuditEvent{CorrelationID: helpers.CorrelationID(r.Context()), ActorID: &actor.ID, ProjectID: &projectID, Action: pro_interfaces.AuditActionCrossProjectTemplateVersionPublish, TargetType: pro_interfaces.AuditTargetCrossProjectTemplateVersion, TargetID: "template-version:" + strconv.Itoa(v.ID), Outcome: pro_interfaces.AuditOutcomeAllowed, Source: pro_interfaces.AuditSourceAPI, Reason: pro_interfaces.AuditReasonCrossProjectTemplateGrantActive, CrossProjectTemplateProvenance: &pro_interfaces.AuditCrossProjectTemplateProvenance{OwnerProjectID: p.ID, TemplateID: id, TemplateVersionID: v.ID, TemplateVersionNumber: v.VersionNumber, TemplateVersionFingerprint: v.ContentFingerprint}})
	}
	status := http.StatusCreated
	if reused {
		status = http.StatusOK
	}
	helpers.WriteJSON(w, status, map[string]any{"id": v.ID, "version_number": v.VersionNumber, "content_fingerprint": v.ContentFingerprint})
}
func (c *crossProjectTemplateController) ListTemplateVersions(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "template_id")
	if !ok {
		return
	}
	params, ok := crossParams(w, r)
	if !ok {
		return
	}
	versions, err := c.service.ListTemplateVersions(p.ID, id, params, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	result := make([]map[string]any, 0, len(versions))
	for _, v := range versions {
		result = append(result, map[string]any{"id": v.ID, "version_number": v.VersionNumber, "content_fingerprint": v.ContentFingerprint, "created": v.Created})
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}
func (c *crossProjectTemplateController) CreateGrant(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "template_id")
	if !ok {
		return
	}
	var v pro_interfaces.CrossProjectTemplateGrantCreate
	if !bindCrossProject(w, r, &v, "consumer_project_id", "min_version", "max_version", "operations", "reason") {
		return
	}
	if v.ConsumerProjectID <= 0 || v.MinVersion <= 0 || v.MaxVersion < v.MinVersion || !v.Operations.IsValid() {
		crossBadRequest(w)
		return
	}
	grant, err := c.service.CreateGrant(p.ID, id, v, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	c.recordGrant(r, pro_interfaces.AuditActionCrossProjectTemplateGrantCreate, grant)
	helpers.WriteJSON(w, http.StatusCreated, safeCrossProjectTemplateGrant(grant))
}
func (c *crossProjectTemplateController) ListGrants(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	params, ok := crossParams(w, r)
	if !ok {
		return
	}
	values, err := c.service.ListGrants(p.ID, params, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	result := make([]crossProjectTemplateGrantView, len(values))
	for i := range values {
		result[i] = safeCrossProjectTemplateGrant(values[i])
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}
func (c *crossProjectTemplateController) UpdateGrant(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "grant_id")
	if !ok {
		return
	}
	var v pro_interfaces.CrossProjectTemplateGrantUpdate
	if !bindCrossProject(w, r, &v, "min_version", "max_version", "operations", "reason", "expected_revision") {
		return
	}
	if v.MinVersion <= 0 || v.MaxVersion < v.MinVersion || !v.Operations.IsValid() || v.ExpectedRevision <= 0 {
		crossBadRequest(w)
		return
	}
	result, err := c.service.UpdateGrant(p.ID, id, v, helpers.UserFromContext(r))
	if err != nil {
		crossWriteServiceError(w, err)
		return
	}
	c.recordGrant(r, pro_interfaces.AuditActionCrossProjectTemplateGrantUpdate, result)
	helpers.WriteJSON(w, http.StatusOK, safeCrossProjectTemplateGrant(result))
}
func (c *crossProjectTemplateController) DeleteGrant(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "grant_id")
	if !ok {
		return
	}
	revision, ok := crossExpectedRevision(w, r)
	if !ok {
		return
	}
	grant, err := c.service.DeleteGrant(p.ID, id, revision, helpers.UserFromContext(r))
	if err != nil {
		crossWriteServiceError(w, err)
		return
	}
	c.recordGrant(r, pro_interfaces.AuditActionCrossProjectTemplateGrantDelete, grant)
	w.WriteHeader(http.StatusNoContent)
}
func (c *crossProjectTemplateController) AcceptGrant(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "grant_id")
	if !ok {
		return
	}
	var v struct {
		ExpectedRevision int `json:"expected_revision"`
	}
	if !bindCrossProject(w, r, &v, "expected_revision") {
		return
	}
	if v.ExpectedRevision <= 0 {
		crossBadRequest(w)
		return
	}
	grant, err := c.service.AcceptGrant(p.ID, id, v.ExpectedRevision, helpers.UserFromContext(r))
	if err != nil {
		crossWriteServiceError(w, err)
		return
	}
	c.recordGrant(r, pro_interfaces.AuditActionCrossProjectTemplateGrantAccept, grant)
	helpers.WriteJSON(w, http.StatusOK, safeCrossProjectTemplateGrant(grant))
}
func (c *crossProjectTemplateController) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "grant_id")
	if !ok {
		return
	}
	var v struct {
		ExpectedRevision int    `json:"expected_revision"`
		Reason           string `json:"reason"`
	}
	if !bindCrossProject(w, r, &v, "expected_revision", "reason") {
		return
	}
	if v.ExpectedRevision <= 0 || strings.TrimSpace(v.Reason) == "" {
		crossBadRequest(w)
		return
	}
	grant, err := c.service.RevokeGrant(p.ID, id, v.ExpectedRevision, v.Reason, helpers.UserFromContext(r))
	if err != nil {
		crossWriteServiceError(w, err)
		return
	}
	c.recordGrant(r, pro_interfaces.AuditActionCrossProjectTemplateGrantRevoke, grant)
	helpers.WriteJSON(w, http.StatusOK, safeCrossProjectTemplateGrant(grant))
}
func (c *crossProjectTemplateController) ListReferences(w http.ResponseWriter, r *http.Request) {
	p := crossProject(r)
	id, ok := crossID(w, r, "grant_id")
	if !ok {
		return
	}
	params, ok := crossParams(w, r)
	if !ok {
		return
	}
	result, grant, err := c.service.ListReferences(p.ID, id, params, helpers.UserFromContext(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	for _, reference := range result {
		c.recordReference(r, grant, reference)
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

var _ pro_interfaces.CrossProjectTemplateController = (*crossProjectTemplateController)(nil)
var _ pro_interfaces.CrossProjectTemplateAuditConfigurer = (*crossProjectTemplateController)(nil)

func (c *crossProjectTemplateController) ConfigureCrossProjectTemplateAudit(audit pro_interfaces.AuditServiceFacade) {
	c.audit = audit
}

func (c *crossProjectTemplateController) recordReference(r *http.Request, grant db.CrossProjectTemplateGrant, reference pro_interfaces.CrossProjectTemplateReferenceView) {
	if c.audit == nil || grant.ID <= 0 || reference.TemplateVersionID <= 0 {
		return
	}
	actor := helpers.UserFromContext(r)
	_ = c.audit.Record(r.Context(), pro_interfaces.AuditEvent{CorrelationID: helpers.CorrelationID(r.Context()), ActorID: &actor.ID, ProjectID: &grant.ConsumerProjectID, Action: pro_interfaces.AuditActionCrossProjectTemplateReferenceResolve, TargetType: pro_interfaces.AuditTargetCrossProjectTemplateGrant, TargetID: "grant:" + strconv.Itoa(grant.ID), Outcome: pro_interfaces.AuditOutcomeAllowed, Source: pro_interfaces.AuditSourceAPI, Reason: pro_interfaces.AuditReasonCrossProjectTemplateGrantActive, CrossProjectTemplateProvenance: &pro_interfaces.AuditCrossProjectTemplateProvenance{OwnerProjectID: grant.OwnerProjectID, ConsumerProjectID: grant.ConsumerProjectID, TemplateID: grant.TemplateID, TemplateVersionID: reference.TemplateVersionID, TemplateVersionNumber: reference.TemplateVersionNumber, TemplateVersionFingerprint: reference.ContentFingerprint, GrantID: grant.ID, GrantRevision: grant.Revision, Operation: int(db.CrossProjectTemplateGrantReference), MinTemplateVersion: grant.MinTemplateVersion, MaxTemplateVersion: grant.MaxTemplateVersion}})
}

func (c *crossProjectTemplateController) recordGrant(r *http.Request, action pro_interfaces.AuditAction, grant db.CrossProjectTemplateGrant) {
	if c.audit == nil {
		return
	}
	actor := helpers.UserFromContext(r)
	projectID := crossProject(r).ID
	_ = c.audit.Record(r.Context(), pro_interfaces.AuditEvent{CorrelationID: helpers.CorrelationID(r.Context()), ActorID: &actor.ID, ProjectID: &projectID, Action: action, TargetType: pro_interfaces.AuditTargetCrossProjectTemplateGrant, TargetID: "grant:" + strconv.Itoa(grant.ID), Outcome: pro_interfaces.AuditOutcomeAllowed, Source: pro_interfaces.AuditSourceAPI, Reason: pro_interfaces.AuditReasonCrossProjectTemplateGrantActive, CrossProjectTemplateProvenance: &pro_interfaces.AuditCrossProjectTemplateProvenance{OwnerProjectID: grant.OwnerProjectID, ConsumerProjectID: grant.ConsumerProjectID, TemplateID: grant.TemplateID, GrantID: grant.ID, GrantRevision: grant.Revision, Operation: int(grant.Operations), MinTemplateVersion: grant.MinTemplateVersion, MaxTemplateVersion: grant.MaxTemplateVersion}})
}
