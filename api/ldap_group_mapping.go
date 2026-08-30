package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

type ldapGroupMappingBody struct {
	ProviderID       string                        `json:"provider_id" binding:"required"`
	GroupExternalID  string                        `json:"group_external_id" binding:"required"`
	Target           pro_interfaces.LDAPRoleTarget `json:"target" binding:"required"`
	Enabled          bool                          `json:"enabled"`
	ExpectedRevision int                           `json:"expected_revision"`
}

func (c *LDAPController) GroupMappings(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	if providerID == "" {
		helpers.WriteErrorStatus(w, "LDAP_PROVIDER_ID_REQUIRED", http.StatusBadRequest)
		return
	}
	mappings, err := c.service.GroupMappings(r.Context(), providerID)
	if err != nil {
		c.recordLDAPGroup(r, pro_interfaces.AuditActionLDAPGroupMappingRead,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError, "provider:"+providerID)
		writeLDAPError(w, err)
		return
	}
	c.recordLDAPGroup(r, pro_interfaces.AuditActionLDAPGroupMappingRead,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), "provider:"+providerID)
	helpers.WriteJSON(w, http.StatusOK, mappings)
}

func (c *LDAPController) SaveGroupMapping(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body ldapGroupMappingBody
	if !helpers.Bind(w, r, &body) {
		c.recordLDAPGroup(r, pro_interfaces.AuditActionLDAPGroupMappingWrite,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput, "provider:invalid")
		return
	}
	mappingID := strings.TrimSpace(mux.Vars(r)["mapping_id"])
	mapping := pro_interfaces.LDAPGroupMapping{
		ID: mappingID, ProviderID: body.ProviderID, GroupExternalID: body.GroupExternalID,
		Target: body.Target, Enabled: body.Enabled, Revision: body.ExpectedRevision,
	}
	saved, err := c.service.SaveGroupMapping(r.Context(), pro_interfaces.LDAPGroupMappingRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, Mapping: mapping,
		ExpectedRevision: body.ExpectedRevision, Now: tz.Now(),
	})
	if err != nil {
		c.recordLDAPGroupError(r, pro_interfaces.AuditActionLDAPGroupMappingWrite, err, body.GroupExternalID)
		writeLDAPError(w, err)
		return
	}
	c.recordLDAPGroup(r, pro_interfaces.AuditActionLDAPGroupMappingWrite,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), saved.GroupExternalID)
	helpers.WriteJSON(w, http.StatusOK, saved)
}

func (c *LDAPController) DeleteGroupMapping(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	expectedRevision, err := strconv.Atoi(r.URL.Query().Get("expected_revision"))
	if providerID == "" || expectedRevision <= 0 || err != nil {
		helpers.WriteErrorStatus(w, "LDAP_GROUP_MAPPING_REVISION_REQUIRED", http.StatusBadRequest)
		return
	}
	mappingID := strings.TrimSpace(mux.Vars(r)["mapping_id"])
	targetID := "provider:" + providerID
	if mappings, listErr := c.service.GroupMappings(r.Context(), providerID); listErr == nil {
		for _, mapping := range mappings {
			if mapping.ID == mappingID {
				targetID = mapping.GroupExternalID
				break
			}
		}
	}
	err = c.service.DeleteGroupMapping(r.Context(), pro_interfaces.LDAPGroupMappingDeleteRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, ProviderID: providerID,
		MappingID: mappingID, ExpectedRevision: expectedRevision, Now: tz.Now(),
	})
	if err != nil {
		c.recordLDAPGroupError(r, pro_interfaces.AuditActionLDAPGroupMappingDelete, err, targetID)
		writeLDAPError(w, err)
		return
	}
	c.recordLDAPGroup(r, pro_interfaces.AuditActionLDAPGroupMappingDelete,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), targetID)
	w.WriteHeader(http.StatusNoContent)
}

func (c *LDAPController) PreviewGroupMappings(w http.ResponseWriter, r *http.Request) {
	c.groupPreviewAction(w, r, false)
}

func (c *LDAPController) ReconcileGroupMappings(w http.ResponseWriter, r *http.Request) {
	c.groupPreviewAction(w, r, true)
}

func (c *LDAPController) groupPreviewAction(w http.ResponseWriter, r *http.Request, apply bool) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		ProviderID string `json:"provider_id" binding:"required"`
	}
	if !helpers.Bind(w, r, &body) {
		return
	}
	actorID := actor.ID
	request := pro_interfaces.LDAPGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: actor.Admin, ProviderID: body.ProviderID,
		Source: "manual", Now: tz.Now(),
	}
	action := pro_interfaces.AuditActionLDAPGroupPreview
	var preview pro_interfaces.LDAPGroupPreview
	var err error
	if apply {
		action = pro_interfaces.AuditActionLDAPGroupReconcile
		preview, err = c.service.ReconcileGroupMappings(r.Context(), request)
	} else {
		preview, err = c.service.PreviewGroupMappings(r.Context(), request)
	}
	targetID := "provider:" + body.ProviderID
	if err != nil {
		c.recordLDAPGroupError(r, action, err, targetID)
		writeLDAPError(w, err)
		return
	}
	c.recordLDAPGroup(r, action, pro_interfaces.AuditOutcomeAllowed,
		string(pro_interfaces.CapabilityReasonActive), targetID)
	helpers.WriteJSON(w, http.StatusOK, preview)
}

func (c *LDAPController) ApplyGroupPreview(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		ProviderID   string `json:"provider_id" binding:"required"`
		PreviewToken string `json:"preview_token" binding:"required"`
	}
	if !helpers.Bind(w, r, &body) {
		return
	}
	request := pro_interfaces.LDAPGroupApplyRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, ProviderID: body.ProviderID, Now: tz.Now(),
	}
	request.Token = body.PreviewToken
	preview, err := c.service.ApplyGroupPreview(r.Context(), request)
	targetID := "provider:" + body.ProviderID
	if err != nil {
		c.recordLDAPGroupError(r, pro_interfaces.AuditActionLDAPGroupApply, err, targetID)
		writeLDAPError(w, err)
		return
	}
	c.recordLDAPGroup(r, pro_interfaces.AuditActionLDAPGroupApply,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), targetID)
	helpers.WriteJSON(w, http.StatusOK, preview)
}

func (c *LDAPController) GroupReconciliationHistory(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	if providerID == "" {
		helpers.WriteErrorStatus(w, "LDAP_PROVIDER_ID_REQUIRED", http.StatusBadRequest)
		return
	}
	history, err := c.service.GroupReconciliationHistory(r.Context(), providerID, 50)
	if err != nil {
		writeLDAPError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, history)
}

func (c *LDAPController) recordLDAPGroupError(
	r *http.Request,
	action pro_interfaces.AuditAction,
	err error,
	targetID string,
) {
	outcome := pro_interfaces.AuditOutcomeFailure
	reason := pro_interfaces.AuditReasonOperationError
	if errors.Is(err, pro_interfaces.ErrLDAPForbidden) ||
		errors.Is(err, pro_interfaces.ErrLDAPGroupPreviewStale) ||
		errors.Is(err, pro_interfaces.ErrLDAPGroupMappingCollision) ||
		errors.Is(err, pro_interfaces.ErrLDAPGroupProtectedAdministrator) ||
		errors.Is(err, pro_interfaces.ErrLDAPGroupUnresolved) {
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonLDAPPolicy
	} else if errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable) ||
		errors.Is(err, pro_interfaces.ErrLDAPReferral) {
		reason = pro_interfaces.AuditReasonProviderError
	}
	c.recordLDAPGroup(r, action, outcome, reason, targetID)
}

func (c *LDAPController) recordLDAPGroup(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
	targetID string,
) {
	if c.audit == nil {
		return
	}
	event := capabilityAuditEvent(r, action, outcome, reason)
	event.TargetType = pro_interfaces.AuditTargetLDAPGroupMapping
	event.TargetID = strings.ToLower(strings.TrimSpace(targetID))
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store LDAP group mapping audit event")
	}
}
