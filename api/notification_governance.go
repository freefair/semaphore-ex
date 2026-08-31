package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const (
	notificationGovernanceBodyLimit = 16 * 1024
	notificationGovernancePageSize  = 25
	notificationGovernancePageMax   = 100
)

// NotificationGovernanceController is a transport-only boundary. Scope comes
// exclusively from the route: callers cannot select a different tenant in JSON.
type NotificationGovernanceController struct {
	service pro_interfaces.NotificationGovernanceServiceFacade
}

func NewNotificationGovernanceController(service pro_interfaces.NotificationGovernanceServiceFacade) *NotificationGovernanceController {
	return &NotificationGovernanceController{service: service}
}

func (c *NotificationGovernanceController) CreateGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.createDestination(w, r, nil)
}
func (c *NotificationGovernanceController) CreateProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.createDestination(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) GetGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.getDestination(w, r, nil)
}
func (c *NotificationGovernanceController) GetProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.getDestination(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) ListGlobalDestinations(w http.ResponseWriter, r *http.Request) {
	c.listDestinations(w, r, nil)
}
func (c *NotificationGovernanceController) ListProjectDestinations(w http.ResponseWriter, r *http.Request) {
	c.listDestinations(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) UpdateGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.updateDestination(w, r, nil)
}
func (c *NotificationGovernanceController) UpdateProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.updateDestination(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) DeleteGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.deleteDestination(w, r, nil)
}
func (c *NotificationGovernanceController) DeleteProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.deleteDestination(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) PauseGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.setDestinationPaused(w, r, nil, true)
}
func (c *NotificationGovernanceController) PauseProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.setDestinationPaused(w, r, c.projectScope(r), true)
}
func (c *NotificationGovernanceController) ResumeGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.setDestinationPaused(w, r, nil, false)
}
func (c *NotificationGovernanceController) ResumeProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.setDestinationPaused(w, r, c.projectScope(r), false)
}
func (c *NotificationGovernanceController) TestGlobalDestination(w http.ResponseWriter, r *http.Request) {
	c.testDestination(w, r, nil)
}
func (c *NotificationGovernanceController) TestProjectDestination(w http.ResponseWriter, r *http.Request) {
	c.testDestination(w, r, c.projectScope(r))
}

func (c *NotificationGovernanceController) CreateGlobalRule(w http.ResponseWriter, r *http.Request) {
	c.createRule(w, r, nil)
}
func (c *NotificationGovernanceController) CreateProjectRule(w http.ResponseWriter, r *http.Request) {
	c.createRule(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) ListGlobalRules(w http.ResponseWriter, r *http.Request) {
	c.listRules(w, r, nil)
}
func (c *NotificationGovernanceController) ListProjectRules(w http.ResponseWriter, r *http.Request) {
	c.listRules(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) UpdateGlobalRule(w http.ResponseWriter, r *http.Request) {
	c.updateRule(w, r, nil)
}
func (c *NotificationGovernanceController) UpdateProjectRule(w http.ResponseWriter, r *http.Request) {
	c.updateRule(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) DeleteGlobalRule(w http.ResponseWriter, r *http.Request) {
	c.deleteRule(w, r, nil)
}
func (c *NotificationGovernanceController) DeleteProjectRule(w http.ResponseWriter, r *http.Request) {
	c.deleteRule(w, r, c.projectScope(r))
}

func (c *NotificationGovernanceController) PreviewGlobalRouting(w http.ResponseWriter, r *http.Request) {
	c.previewRouting(w, r, nil)
}
func (c *NotificationGovernanceController) PreviewProjectRouting(w http.ResponseWriter, r *http.Request) {
	c.previewRouting(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) GlobalHistory(w http.ResponseWriter, r *http.Request) {
	c.history(w, r, nil)
}
func (c *NotificationGovernanceController) ProjectHistory(w http.ResponseWriter, r *http.Request) {
	c.history(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) GlobalEventHistory(w http.ResponseWriter, r *http.Request) {
	c.eventHistory(w, r, nil)
}
func (c *NotificationGovernanceController) ProjectEventHistory(w http.ResponseWriter, r *http.Request) {
	c.eventHistory(w, r, c.projectScope(r))
}
func (c *NotificationGovernanceController) RetryGlobalDelivery(w http.ResponseWriter, r *http.Request) {
	c.retryDelivery(w, r, nil)
}
func (c *NotificationGovernanceController) RetryProjectDelivery(w http.ResponseWriter, r *http.Request) {
	c.retryDelivery(w, r, c.projectScope(r))
}

func (c *NotificationGovernanceController) createDestination(w http.ResponseWriter, r *http.Request, projectID *int) {
	var input pro_interfaces.NotificationDestinationInput
	if !decodeNotificationJSON(w, r, &input) {
		return
	}
	created, err := c.service.CreateDestination(r.Context(), projectID, input)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, created)
}

func (c *NotificationGovernanceController) getDestination(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "destination_id")
	if !ok {
		return
	}
	destination, err := c.service.GetDestination(r.Context(), projectID, id)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, destination)
}

func (c *NotificationGovernanceController) listDestinations(w http.ResponseWriter, r *http.Request, projectID *int) {
	params, ok := notificationPageParams(w, r)
	if !ok {
		return
	}
	destinations, err := c.service.ListDestinations(r.Context(), projectID, params)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, destinations)
}

func (c *NotificationGovernanceController) updateDestination(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "destination_id")
	if !ok {
		return
	}
	var request struct {
		pro_interfaces.NotificationDestinationInput
		Revision int `json:"revision"`
	}
	if !decodeNotificationJSON(w, r, &request) {
		return
	}
	updated, err := c.service.UpdateDestination(r.Context(), projectID, id, request.Revision, request.NotificationDestinationInput)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *NotificationGovernanceController) deleteDestination(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "destination_id")
	if !ok {
		return
	}
	revision, ok := notificationRevision(w, r)
	if !ok {
		return
	}
	if err := c.service.DeleteDestination(r.Context(), projectID, id, revision); err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *NotificationGovernanceController) setDestinationPaused(w http.ResponseWriter, r *http.Request, projectID *int, paused bool) {
	id, ok := notificationRouteID(w, r, "destination_id")
	if !ok {
		return
	}
	var request struct {
		Revision int `json:"revision"`
	}
	if !decodeNotificationJSON(w, r, &request) {
		return
	}
	updated, err := c.service.SetDestinationPaused(r.Context(), projectID, id, request.Revision, paused)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *NotificationGovernanceController) testDestination(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "destination_id")
	if !ok {
		return
	}
	delivery, err := c.service.EnqueueTestDelivery(r.Context(), projectID, id)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, delivery)
}

func (c *NotificationGovernanceController) createRule(w http.ResponseWriter, r *http.Request, projectID *int) {
	var input pro_interfaces.NotificationRuleInput
	if !decodeNotificationJSON(w, r, &input) {
		return
	}
	created, err := c.service.CreateRule(r.Context(), projectID, input)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, created)
}

func (c *NotificationGovernanceController) listRules(w http.ResponseWriter, r *http.Request, projectID *int) {
	params, ok := notificationPageParams(w, r)
	if !ok {
		return
	}
	rules, err := c.service.ListRules(r.Context(), projectID, params)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, rules)
}

func (c *NotificationGovernanceController) updateRule(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "rule_id")
	if !ok {
		return
	}
	var request struct {
		pro_interfaces.NotificationRuleInput
		Revision int `json:"revision"`
	}
	if !decodeNotificationJSON(w, r, &request) {
		return
	}
	updated, err := c.service.UpdateRule(r.Context(), projectID, id, request.Revision, request.NotificationRuleInput)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *NotificationGovernanceController) deleteRule(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "rule_id")
	if !ok {
		return
	}
	revision, ok := notificationRevision(w, r)
	if !ok {
		return
	}
	if err := c.service.DeleteRule(r.Context(), projectID, id, revision); err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *NotificationGovernanceController) previewRouting(w http.ResponseWriter, r *http.Request, projectID *int) {
	var event pro_interfaces.NotificationEvent
	if !decodeNotificationJSON(w, r, &event) {
		return
	}
	preview, err := c.service.PreviewRouting(r.Context(), projectID, event)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, preview)
}

func (c *NotificationGovernanceController) history(w http.ResponseWriter, r *http.Request, projectID *int) {
	params, ok := notificationPageParams(w, r)
	if !ok {
		return
	}
	deliveries, err := c.service.DeliveryHistory(r.Context(), projectID, params)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, deliveries)
}

func (c *NotificationGovernanceController) eventHistory(w http.ResponseWriter, r *http.Request, projectID *int) {
	params, ok := notificationPageParams(w, r)
	if !ok {
		return
	}
	events, err := c.service.EventHistory(r.Context(), projectID, params)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, events)
}

func (c *NotificationGovernanceController) retryDelivery(w http.ResponseWriter, r *http.Request, projectID *int) {
	id, ok := notificationRouteID(w, r, "delivery_id")
	if !ok {
		return
	}
	delivery, err := c.service.RetryDelivery(r.Context(), projectID, id)
	if err != nil {
		writeNotificationGovernanceError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, delivery)
}

func (c *NotificationGovernanceController) projectScope(r *http.Request) *int {
	project, ok := helpers.GetFromContext(r, "project").(db.Project)
	if !ok || project.ID <= 0 {
		return nil
	}
	id := project.ID
	return &id
}

func decodeNotificationJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, notificationGovernanceBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		helpers.WriteErrorStatus(w, "Invalid notification governance input", http.StatusBadRequest)
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		helpers.WriteErrorStatus(w, "Invalid notification governance input", http.StatusBadRequest)
		return false
	}
	return true
}

func notificationRouteID(w http.ResponseWriter, r *http.Request, name string) (int, bool) {
	id, err := strconv.Atoi(mux.Vars(r)[name])
	if err != nil || id <= 0 {
		helpers.WriteErrorStatus(w, "Invalid notification identifier", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func notificationRevision(w http.ResponseWriter, r *http.Request) (int, bool) {
	var request struct {
		Revision int `json:"revision"`
	}
	if !decodeNotificationJSON(w, r, &request) {
		return 0, false
	}
	if request.Revision <= 0 {
		helpers.WriteErrorStatus(w, "Invalid notification governance input", http.StatusBadRequest)
		return 0, false
	}
	return request.Revision, true
}

func notificationPageParams(w http.ResponseWriter, r *http.Request) (db.RetrieveQueryParams, bool) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "count" && key != "offset") || len(values) != 1 {
			helpers.WriteErrorStatus(w, "Invalid pagination", http.StatusBadRequest)
			return db.RetrieveQueryParams{}, false
		}
	}
	count, offset := notificationGovernancePageSize, 0
	if raw := query.Get("count"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > notificationGovernancePageMax {
			helpers.WriteErrorStatus(w, "Invalid pagination", http.StatusBadRequest)
			return db.RetrieveQueryParams{}, false
		}
		count = parsed
	}
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			helpers.WriteErrorStatus(w, "Invalid pagination", http.StatusBadRequest)
			return db.RetrieveQueryParams{}, false
		}
		offset = parsed
	}
	return db.RetrieveQueryParams{Count: count, Offset: offset}, true
}

func writeNotificationGovernanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pro_interfaces.ErrNotificationUnavailable), errors.Is(err, pro_interfaces.ErrNotificationEncryptionRequired):
		helpers.WriteErrorStatus(w, "Notification governance is unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, pro_interfaces.ErrNotificationInvalidInput):
		helpers.WriteErrorStatus(w, "Invalid notification governance input", http.StatusBadRequest)
	case errors.Is(err, pro_interfaces.ErrNotificationDestinationMissing), errors.Is(err, pro_interfaces.ErrNotificationRuleMissing):
		helpers.WriteErrorStatus(w, "Notification resource not found", http.StatusNotFound)
	case errors.Is(err, pro_interfaces.ErrNotificationRevisionConflict), errors.Is(err, pro_interfaces.ErrNotificationRetryUnavailable),
		errors.Is(err, pro_interfaces.ErrNotificationDestinationNotConfigured), errors.Is(err, pro_interfaces.ErrNotificationDestinationDisabled), errors.Is(err, pro_interfaces.ErrNotificationDestinationPaused):
		helpers.WriteErrorStatus(w, "Notification operation cannot be completed", http.StatusConflict)
	default:
		helpers.WriteErrorStatus(w, "Notification governance is unavailable", http.StatusServiceUnavailable)
	}
}
