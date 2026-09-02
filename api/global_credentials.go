package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const globalCredentialBodyLimit = 8 * 1024

type GlobalCredentialController struct {
	service pro_interfaces.GlobalCredentialServiceFacade
}

func NewGlobalCredentialController(service pro_interfaces.GlobalCredentialServiceFacade) *GlobalCredentialController {
	return &GlobalCredentialController{service: service}
}

func (c *GlobalCredentialController) Create(w http.ResponseWriter, r *http.Request) {
	var input pro_interfaces.GlobalCredentialInput
	if !decodeGlobalCredentialJSON(w, r, &input) {
		return
	}
	actor := helpers.UserFromContext(r)
	value, err := c.service.CreateGlobalCredential(r.Context(), actor.ID, input)
	c.write(w, http.StatusCreated, value, err)
}
func (c *GlobalCredentialController) List(w http.ResponseWriter, r *http.Request) {
	params, ok := globalCredentialPage(w, r)
	if !ok {
		return
	}
	value, err := c.service.ListGlobalCredentials(r.Context(), params)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	value, err := c.service.GetGlobalCredential(r.Context(), id)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	var request struct {
		pro_interfaces.GlobalCredentialMetadataInput
		Revision int `json:"revision"`
	}
	if !decodeGlobalCredentialJSON(w, r, &request) {
		return
	}
	actor := helpers.UserFromContext(r)
	value, err := c.service.UpdateGlobalCredential(r.Context(), actor.ID, id, request.Revision, request.GlobalCredentialMetadataInput)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) SetEnabled(w http.ResponseWriter, r *http.Request) {
	id, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	var request struct {
		Revision int  `json:"revision"`
		Enabled  bool `json:"enabled"`
	}
	if !decodeGlobalCredentialJSON(w, r, &request) {
		return
	}
	actor := helpers.UserFromContext(r)
	value, err := c.service.SetGlobalCredentialEnabled(r.Context(), actor.ID, id, request.Revision, request.Enabled)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) Rotate(w http.ResponseWriter, r *http.Request) {
	id, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	var request struct {
		Revision int                                          `json:"revision"`
		Material pro_interfaces.GlobalCredentialMaterialInput `json:"material"`
	}
	if !decodeGlobalCredentialJSON(w, r, &request) {
		return
	}
	actor := helpers.UserFromContext(r)
	value, err := c.service.RotateGlobalCredential(r.Context(), actor.ID, id, request.Revision, request.Material)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	revision, ok := globalCredentialRevision(w, r)
	if !ok {
		return
	}
	err := c.service.DeleteGlobalCredential(r.Context(), helpers.UserFromContext(r).ID, id, revision)
	c.write(w, http.StatusNoContent, nil, err)
}
func (c *GlobalCredentialController) ListGranted(w http.ResponseWriter, r *http.Request) {
	projectID, ok := globalCredentialID(w, r, "project_id")
	if !ok {
		return
	}
	params, ok := globalCredentialPage(w, r)
	if !ok {
		return
	}
	value, err := c.service.ListGrantedCredentials(r.Context(), projectID, params)
	c.write(w, http.StatusOK, value, err)
}

func (c *GlobalCredentialController) ListUsage(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	query, ok := globalCredentialUsagePage(w, r, true)
	if !ok {
		return
	}
	value, err := c.service.ListGlobalCredentialUsage(r.Context(), credentialID, query)
	c.write(w, http.StatusOK, value, err)
}

func (c *GlobalCredentialController) GetImpact(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	if _, ok = globalCredentialQuery(w, r); !ok {
		return
	}
	value, err := c.service.GetGlobalCredentialImpact(r.Context(), credentialID)
	c.write(w, http.StatusOK, value, err)
}

func (c *GlobalCredentialController) ListTaskUsage(w http.ResponseWriter, r *http.Request) {
	projectID, ok := globalCredentialID(w, r, "project_id")
	if !ok {
		return
	}
	taskID, ok := globalCredentialID(w, r, "task_id")
	if !ok {
		return
	}
	query, ok := globalCredentialUsagePage(w, r, false)
	if !ok {
		return
	}
	value, err := c.service.ListTaskGlobalCredentialUsage(r.Context(), projectID, taskID, query)
	c.write(w, http.StatusOK, value, err)
}

// ListGrantProjects is deliberately separate from /projects: a delegated
// grant administrator needs a target selector, not project configuration.
func (c *GlobalCredentialController) ListGrantProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := c.service.ListGlobalCredentialGrantProjects(r.Context())
	c.write(w, http.StatusOK, projects, err)
}
func (c *GlobalCredentialController) CreateGrant(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	var input pro_interfaces.GlobalCredentialGrantInput
	if !decodeGlobalCredentialJSON(w, r, &input) {
		return
	}
	value, err := c.service.CreateGlobalCredentialGrant(r.Context(), helpers.UserFromContext(r).ID, credentialID, input)
	c.write(w, http.StatusCreated, value, err)
}
func (c *GlobalCredentialController) ListGrants(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	params, ok := globalCredentialPage(w, r)
	if !ok {
		return
	}
	value, err := c.service.ListGlobalCredentialGrants(r.Context(), credentialID, params)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) UpdateGrant(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	grantID, ok := globalCredentialID(w, r, "grant_id")
	if !ok {
		return
	}
	var request struct {
		pro_interfaces.GlobalCredentialGrantInput
		Revision int `json:"revision"`
	}
	if !decodeGlobalCredentialJSON(w, r, &request) {
		return
	}
	value, err := c.service.UpdateGlobalCredentialGrant(r.Context(), helpers.UserFromContext(r).ID, credentialID, grantID, request.Revision, request.GlobalCredentialGrantInput)
	c.write(w, http.StatusOK, value, err)
}

func (c *GlobalCredentialController) RevokeGrant(w http.ResponseWriter, r *http.Request) {
	c.setGrantStatus(w, r, db.GlobalCredentialGrantStatusRevoked)
}

func (c *GlobalCredentialController) RestoreGrant(w http.ResponseWriter, r *http.Request) {
	c.setGrantStatus(w, r, db.GlobalCredentialGrantStatusActive)
}

func (c *GlobalCredentialController) setGrantStatus(w http.ResponseWriter, r *http.Request, status db.GlobalCredentialGrantStatus) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	grantID, ok := globalCredentialID(w, r, "grant_id")
	if !ok {
		return
	}
	var request struct {
		Revision int `json:"revision"`
	}
	if !decodeGlobalCredentialJSON(w, r, &request) {
		return
	}
	value, err := c.service.SetGlobalCredentialGrantStatus(r.Context(), helpers.UserFromContext(r).ID, credentialID, grantID, request.Revision, status)
	c.write(w, http.StatusOK, value, err)
}
func (c *GlobalCredentialController) DeleteGrant(w http.ResponseWriter, r *http.Request) {
	credentialID, ok := globalCredentialID(w, r, "credential_id")
	if !ok {
		return
	}
	grantID, ok := globalCredentialID(w, r, "grant_id")
	if !ok {
		return
	}
	revision, ok := globalCredentialRevision(w, r)
	if !ok {
		return
	}
	err := c.service.DeleteGlobalCredentialGrant(r.Context(), helpers.UserFromContext(r).ID, credentialID, grantID, revision)
	c.write(w, http.StatusNoContent, nil, err)
}
func (c *GlobalCredentialController) write(w http.ResponseWriter, status int, value any, err error) {
	if err == nil {
		if status == http.StatusNoContent {
			w.WriteHeader(status)
		} else {
			helpers.WriteJSON(w, status, value)
		}
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialNotFound) {
		helpers.WriteErrorStatus(w, "Global credential was not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialNotAvailable) || errors.Is(err, pro_interfaces.ErrGlobalCredentialEncryptionRequired) {
		helpers.WriteErrorStatus(w, "Global credential service is unavailable", http.StatusServiceUnavailable)
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialRevisionConflict) {
		helpers.WriteErrorStatus(w, "Global credential revision conflict", http.StatusConflict)
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialGrantConflict) {
		helpers.WriteErrorStatus(w, "Global credential grant conflict", http.StatusConflict)
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialGrantExists) {
		helpers.WriteErrorStatus(w, "Global credential grant already exists", http.StatusConflict)
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialDependencyConflict) {
		helpers.WriteErrorStatus(w, "Global credential dependency conflict", http.StatusConflict)
		return
	}
	if errors.Is(err, pro_interfaces.ErrGlobalCredentialInvalidInput) || errors.Is(err, db.ErrInvalidOperation) {
		helpers.WriteErrorStatus(w, "Invalid global credential input", http.StatusBadRequest)
		return
	}
	helpers.WriteErrorStatus(w, "Global credential service failure", http.StatusInternalServerError)
}
func decodeGlobalCredentialJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, globalCredentialBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		helpers.WriteErrorStatus(w, "Invalid global credential input", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		helpers.WriteErrorStatus(w, "Invalid global credential input", http.StatusBadRequest)
		return false
	}
	return true
}
func globalCredentialID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	value, err := strconv.Atoi(mux.Vars(r)[name])
	if err != nil || value <= 0 {
		helpers.WriteErrorStatus(w, "Invalid global credential identifier", http.StatusBadRequest)
		return 0, false
	}
	return value, true
}
func globalCredentialRevision(w http.ResponseWriter, r *http.Request) (int, bool) {
	values, ok := globalCredentialQuery(w, r, "expected_revision")
	if !ok {
		return 0, false
	}
	value, err := strconv.Atoi(values.Get("expected_revision"))
	if err != nil || value <= 0 {
		helpers.WriteErrorStatus(w, "Invalid global credential revision", http.StatusBadRequest)
		return 0, false
	}
	return value, true
}
func globalCredentialPage(w http.ResponseWriter, r *http.Request) (db.RetrieveQueryParams, bool) {
	query, ok := globalCredentialQuery(w, r, "count", "offset")
	if !ok {
		return db.RetrieveQueryParams{}, false
	}
	params := db.RetrieveQueryParams{Count: 25}
	if raw := query.Get("count"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			helpers.WriteErrorStatus(w, "Invalid global credential page", http.StatusBadRequest)
			return params, false
		}
		params.Count = value
	}
	if raw := query.Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			helpers.WriteErrorStatus(w, "Invalid global credential page", http.StatusBadRequest)
			return params, false
		}
		params.Offset = value
	}
	return params, true
}

func globalCredentialUsagePage(w http.ResponseWriter, r *http.Request, global bool) (pro_interfaces.GlobalCredentialUsageQuery, bool) {
	allowed := []string{"count", "before_id", "outcome"}
	if global {
		allowed = append(allowed, "project_id", "task_id")
	}
	values, ok := globalCredentialQuery(w, r, allowed...)
	query := pro_interfaces.GlobalCredentialUsageQuery{Count: 25}
	if !ok {
		return query, false
	}
	parsePositive := func(name string, optional bool) (*int, bool) {
		raw := values.Get(name)
		if raw == "" && optional {
			return nil, true
		}
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return nil, false
		}
		return &value, true
	}
	if raw := values.Get("count"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > pro_interfaces.GlobalCredentialUsagePageMax {
			helpers.WriteErrorStatus(w, "Invalid global credential usage page", http.StatusBadRequest)
			return query, false
		}
		query.Count = value
	}
	if raw := values.Get("before_id"); raw != "" {
		value, valid := parsePositive("before_id", false)
		if !valid {
			helpers.WriteErrorStatus(w, "Invalid global credential usage page", http.StatusBadRequest)
			return query, false
		}
		query.BeforeID = *value
	}
	if raw := values.Get("outcome"); raw != "" {
		outcome := pro_interfaces.GlobalCredentialResolutionOutcome(raw)
		query.Outcome = &outcome
	}
	if global {
		var valid bool
		query.ProjectID, valid = parsePositive("project_id", true)
		if !valid {
			helpers.WriteErrorStatus(w, "Invalid global credential usage filter", http.StatusBadRequest)
			return query, false
		}
		query.TaskID, valid = parsePositive("task_id", true)
		if !valid {
			helpers.WriteErrorStatus(w, "Invalid global credential usage filter", http.StatusBadRequest)
			return query, false
		}
	}
	if query.Validate() != nil {
		helpers.WriteErrorStatus(w, "Invalid global credential usage filter", http.StatusBadRequest)
		return query, false
	}
	return query, true
}

// globalCredentialQuery accepts precisely one bounded value per known key.
// Unlike url.Values.Get, this rejects ambiguous duplicate keys before they can
// make a proxy and the application interpret a mutation differently.
func globalCredentialQuery(w http.ResponseWriter, r *http.Request, allowed ...string) (url.Values, bool) {
	query := r.URL.Query()
	valid := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		valid[key] = struct{}{}
	}
	for key, values := range query {
		if _, ok := valid[key]; !ok || len(values) != 1 || values[0] == "" {
			helpers.WriteErrorStatus(w, "Invalid global credential query", http.StatusBadRequest)
			return nil, false
		}
	}
	for key := range valid {
		if values, exists := query[key]; exists && len(values) != 1 {
			helpers.WriteErrorStatus(w, "Invalid global credential query", http.StatusBadRequest)
			return nil, false
		}
	}
	return query, true
}
