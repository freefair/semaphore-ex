package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

const (
	auditWebhookConfigBodyLimit = 8 * 1024
	auditWebhookDefaultPageSize = 25
	auditWebhookMaxPageSize     = 100
)

type AuditWebhookController struct {
	service pro_interfaces.AuditWebhookServiceFacade
	audit   pro_interfaces.AuditServiceFacade
}

func NewAuditWebhookController(service pro_interfaces.AuditWebhookServiceFacade, audit pro_interfaces.AuditServiceFacade) *AuditWebhookController {
	return &AuditWebhookController{service: service, audit: audit}
}

func (c *AuditWebhookController) GetConfiguration(w http.ResponseWriter, r *http.Request) {
	config, err := c.service.Configuration(r.Context())
	if err != nil {
		writeAuditWebhookError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, config)
}

func (c *AuditWebhookController) Configure(w http.ResponseWriter, r *http.Request) {
	var input pro_interfaces.AuditWebhookConfigInput
	if err := decodeAuditWebhookConfig(w, r, &input); err != nil {
		c.record(r, pro_interfaces.AuditActionWebhookConfigure, pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput)
		helpers.WriteErrorStatus(w, "Invalid audit webhook configuration", http.StatusBadRequest)
		return
	}
	config, err := c.service.Configure(r.Context(), input)
	if err != nil {
		outcome, reason := pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError
		if errors.Is(err, pro_interfaces.ErrAuditWebhookInvalidInput) {
			outcome, reason = pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonInvalidInput
		}
		c.record(r, pro_interfaces.AuditActionWebhookConfigure, outcome, reason)
		writeAuditWebhookError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionWebhookConfigure, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive))
	helpers.WriteJSON(w, http.StatusOK, config)
}

func (c *AuditWebhookController) TestDelivery(w http.ResponseWriter, r *http.Request) {
	delivery, err := c.service.TestDelivery(r.Context())
	if err != nil {
		c.record(r, pro_interfaces.AuditActionWebhookTest, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		writeAuditWebhookError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionWebhookTest, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive))
	helpers.WriteJSON(w, http.StatusCreated, delivery)
}

func (c *AuditWebhookController) Pause(w http.ResponseWriter, r *http.Request) {
	c.setPaused(w, r, true, pro_interfaces.AuditActionWebhookPause)
}

func (c *AuditWebhookController) Resume(w http.ResponseWriter, r *http.Request) {
	c.setPaused(w, r, false, pro_interfaces.AuditActionWebhookResume)
}

func (c *AuditWebhookController) setPaused(w http.ResponseWriter, r *http.Request, paused bool, action pro_interfaces.AuditAction) {
	config, err := c.service.SetPaused(r.Context(), paused)
	if err != nil {
		c.record(r, action, pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError)
		writeAuditWebhookError(w, err)
		return
	}
	c.record(r, action, pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive))
	helpers.WriteJSON(w, http.StatusOK, config)
}

func (c *AuditWebhookController) History(w http.ResponseWriter, r *http.Request) {
	params, err := auditWebhookPageParams(r)
	if err != nil {
		helpers.WriteErrorStatus(w, "Invalid pagination", http.StatusBadRequest)
		return
	}
	deliveries, err := c.service.DeliveryHistory(r.Context(), params)
	if err != nil {
		writeAuditWebhookError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, deliveries)
}

func (c *AuditWebhookController) record(r *http.Request, action pro_interfaces.AuditAction, outcome pro_interfaces.AuditOutcome, reason string) {
	if c.audit == nil {
		return
	}
	event := capabilityAuditEvent(r, action, outcome, reason)
	event.TargetType = pro_interfaces.AuditTargetWebhook
	event.TargetID = "audit_webhook"
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store audit webhook administration event")
	}
}

func decodeAuditWebhookConfig(w http.ResponseWriter, r *http.Request, input *pro_interfaces.AuditWebhookConfigInput) error {
	r.Body = http.MaxBytesReader(w, r.Body, auditWebhookConfigBodyLimit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func auditWebhookPageParams(r *http.Request) (db.RetrieveQueryParams, error) {
	count := auditWebhookDefaultPageSize
	offset := 0
	var err error
	if raw := r.URL.Query().Get("count"); raw != "" {
		count, err = strconv.Atoi(raw)
		if err != nil || count < 1 || count > auditWebhookMaxPageSize {
			return db.RetrieveQueryParams{}, fmt.Errorf("invalid count")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return db.RetrieveQueryParams{}, fmt.Errorf("invalid offset")
		}
	}
	return db.RetrieveQueryParams{Count: count, Offset: offset}, nil
}

func writeAuditWebhookError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pro_interfaces.ErrAuditWebhookUnavailable):
		helpers.WriteErrorStatus(w, "Audit webhook export is unavailable", http.StatusNotFound)
	case errors.Is(err, pro_interfaces.ErrAuditWebhookInvalidInput):
		helpers.WriteErrorStatus(w, "Invalid audit webhook configuration", http.StatusBadRequest)
	case errors.Is(err, pro_interfaces.ErrAuditWebhookNotConfigured):
		helpers.WriteErrorStatus(w, "Audit webhook is not configured", http.StatusConflict)
	default:
		helpers.WriteErrorStatus(w, "Audit webhook operation failed", http.StatusInternalServerError)
	}
}
