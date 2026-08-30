package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

type OIDCGroupMappingController struct {
	service pro_interfaces.OIDCGroupMappingService
	audit   pro_interfaces.AuditServiceFacade
}

func NewOIDCGroupMappingController(
	service pro_interfaces.OIDCGroupMappingService,
	audit pro_interfaces.AuditServiceFacade,
) *OIDCGroupMappingController {
	return &OIDCGroupMappingController{service: service, audit: audit}
}

func (c *OIDCGroupMappingController) Providers(w http.ResponseWriter, r *http.Request) {
	if err := c.service.Available(r.Context()); err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	providers := make([]pro_interfaces.OIDCGroupProviderConfiguration, 0, len(util.Config.OidcProviders))
	seenProviderIDs := make(map[string]bool, len(util.Config.OidcProviders))
	for id, provider := range util.Config.OidcProviders {
		normalizedID := normalizeConfiguredOIDCProviderID(id)
		if seenProviderIDs[normalizedID] {
			writeOIDCGroupMappingError(w,
				common_errors.NewValidationError("OIDC provider IDs are ambiguous after normalization"))
			return
		}
		seenProviderIDs[normalizedID] = true
		configuration, configured, err := oidcGroupClaimConfiguration(provider)
		if err != nil {
			writeOIDCGroupMappingError(w, err)
			return
		}
		if !configured {
			configuration = pro_interfaces.OIDCGroupClaimConfiguration{}
		}
		providers = append(providers, pro_interfaces.OIDCGroupProviderConfiguration{
			ID: normalizedID, DisplayName: provider.DisplayName,
			ClaimConfiguration: configuration,
		})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	helpers.WriteJSON(w, http.StatusOK, providers)
}

func (c *OIDCGroupMappingController) GroupMappings(w http.ResponseWriter, r *http.Request) {
	providerID := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider_id")))
	if _, _, err := configuredOIDCGroupProvider(providerID); err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	mappings, err := c.service.GroupMappings(r.Context(), providerID)
	if err != nil {
		c.record(r, pro_interfaces.AuditActionOIDCGroupMappingRead,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError, providerID)
		writeOIDCGroupMappingError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionOIDCGroupMappingRead,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), providerID)
	helpers.WriteJSON(w, http.StatusOK, mappings)
}

func (c *OIDCGroupMappingController) SaveGroupMapping(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		ProviderID       string                        `json:"provider_id" binding:"required"`
		ClaimValue       string                        `json:"claim_value" binding:"required"`
		Target           pro_interfaces.OIDCRoleTarget `json:"target" binding:"required"`
		Enabled          bool                          `json:"enabled"`
		ExpectedRevision int                           `json:"expected_revision"`
	}
	if !helpers.Bind(w, r, &body) {
		return
	}
	providerID, configuration, err := configuredOIDCGroupProvider(body.ProviderID)
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	mappingID := strings.TrimSpace(mux.Vars(r)["mapping_id"])
	saved, err := c.service.SaveGroupMapping(r.Context(), pro_interfaces.OIDCGroupMappingRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, Configuration: configuration,
		ExpectedRevision: body.ExpectedRevision, Now: tz.Now(),
		Mapping: pro_interfaces.OIDCGroupMapping{
			ID: mappingID, ProviderID: providerID, ClaimValue: body.ClaimValue,
			Target: body.Target, Enabled: body.Enabled, Revision: body.ExpectedRevision,
		},
	})
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionOIDCGroupMappingWrite, err, providerID)
		writeOIDCGroupMappingError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionOIDCGroupMappingWrite,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), providerID)
	helpers.WriteJSON(w, http.StatusOK, saved)
}

func (c *OIDCGroupMappingController) DeleteGroupMapping(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	providerID, _, err := configuredOIDCGroupProvider(r.URL.Query().Get("provider_id"))
	expectedRevision, revisionErr := strconv.Atoi(r.URL.Query().Get("expected_revision"))
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	if revisionErr != nil || expectedRevision <= 0 {
		helpers.WriteErrorStatus(w, "OIDC_GROUP_MAPPING_REVISION_REQUIRED", http.StatusBadRequest)
		return
	}
	err = c.service.DeleteGroupMapping(r.Context(), pro_interfaces.OIDCGroupMappingDeleteRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, ProviderID: providerID,
		MappingID: mux.Vars(r)["mapping_id"], ExpectedRevision: expectedRevision, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionOIDCGroupMappingDelete, err, providerID)
		writeOIDCGroupMappingError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionOIDCGroupMappingDelete,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), providerID)
	w.WriteHeader(http.StatusNoContent)
}

func (c *OIDCGroupMappingController) PreviewGroupMappings(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		ProviderID string `json:"provider_id" binding:"required"`
		UserID     int    `json:"user_id" binding:"required"`
		Claim      any    `json:"claim" binding:"required"`
	}
	if !helpers.Bind(w, r, &body) {
		return
	}
	providerID, configuration, err := configuredOIDCGroupProvider(body.ProviderID)
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	claim, err := pro_interfaces.ParseOIDCGroupClaim(
		oidcFixtureClaims(configuration.Path, body.Claim), configuration)
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionOIDCGroupPreview, err, providerID)
		writeOIDCGroupMappingError(w, err)
		return
	}
	actorID := actor.ID
	preview, err := c.service.PreviewGroupMappings(r.Context(), pro_interfaces.OIDCGroupPreviewRequest{
		ActorID: &actorID, ActorIsAdmin: actor.Admin, ProviderID: providerID,
		Configuration: configuration, Claim: claim, UserID: body.UserID,
		Source: "manual", Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionOIDCGroupPreview, err, providerID)
		writeOIDCGroupMappingError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionOIDCGroupPreview,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive), providerID)
	helpers.WriteJSON(w, http.StatusOK, preview)
}

func (c *OIDCGroupMappingController) GroupReconciliationHistory(w http.ResponseWriter, r *http.Request) {
	providerID, _, err := configuredOIDCGroupProvider(r.URL.Query().Get("provider_id"))
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	history, err := c.service.GroupReconciliationHistory(r.Context(), providerID, 50)
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, history)
}

func (c *OIDCGroupMappingController) EffectiveGroupAssignments(w http.ResponseWriter, r *http.Request) {
	providerID, _, err := configuredOIDCGroupProvider(r.URL.Query().Get("provider_id"))
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	assignments, err := c.service.EffectiveGroupAssignments(r.Context(), providerID)
	if err != nil {
		writeOIDCGroupMappingError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, assignments)
}

func configuredOIDCGroupProvider(
	providerID string,
) (string, pro_interfaces.OIDCGroupClaimConfiguration, error) {
	providerID = normalizeConfiguredOIDCProviderID(providerID)
	var provider util.OidcProvider
	found := false
	for configuredID, candidate := range util.Config.OidcProviders {
		if normalizeConfiguredOIDCProviderID(configuredID) != providerID {
			continue
		}
		if found {
			return "", pro_interfaces.OIDCGroupClaimConfiguration{},
				common_errors.NewValidationError("OIDC provider ID is ambiguous after normalization")
		}
		provider = candidate
		found = true
	}
	if !found {
		return "", pro_interfaces.OIDCGroupClaimConfiguration{},
			common_errors.NewValidationError("OIDC group mapping provider does not exist")
	}
	configuration, configured, err := oidcGroupClaimConfiguration(provider)
	if err != nil {
		return "", pro_interfaces.OIDCGroupClaimConfiguration{}, err
	}
	if !configured {
		return "", pro_interfaces.OIDCGroupClaimConfiguration{},
			common_errors.NewValidationError("OIDC provider has no group claim path")
	}
	return providerID, configuration, nil
}

func normalizeConfiguredOIDCProviderID(providerID string) string {
	return strings.ToLower(strings.TrimSpace(providerID))
}

func oidcFixtureClaims(path string, claim any) map[string]any {
	root := make(map[string]any)
	current := root
	segments := strings.Split(path, ".")
	for index, segment := range segments {
		if index == len(segments)-1 {
			current[segment] = claim
			break
		}
		next := make(map[string]any)
		current[segment] = next
		current = next
	}
	return root
}

func (c *OIDCGroupMappingController) recordError(
	r *http.Request,
	action pro_interfaces.AuditAction,
	err error,
	providerID string,
) {
	outcome := pro_interfaces.AuditOutcomeFailure
	reason := pro_interfaces.AuditReasonOperationError
	if errors.Is(err, pro_interfaces.ErrOIDCGroupMappingForbidden) ||
		errors.Is(err, pro_interfaces.ErrOIDCGroupMappingPreviewStale) ||
		errors.Is(err, pro_interfaces.ErrOIDCGroupMappingCollision) ||
		errors.Is(err, pro_interfaces.ErrOIDCGroupProtectedAdministrator) {
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonOIDCPolicy
	}
	c.record(r, action, outcome, reason, providerID)
}

func (c *OIDCGroupMappingController) record(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
	providerID string,
) {
	if c.audit == nil {
		return
	}
	event := capabilityAuditEvent(r, action, outcome, reason)
	event.TargetType = pro_interfaces.AuditTargetOIDCGroupMapping
	event.TargetID = "provider:" + strings.ToLower(strings.TrimSpace(providerID))
	if err := c.audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store OIDC group mapping audit event")
	}
}

func writeOIDCGroupMappingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pro_interfaces.ErrOIDCGroupMappingUnavailable):
		helpers.WriteErrorStatus(w, "OIDC_GROUP_MAPPING_UNAVAILABLE", http.StatusNotFound)
	case errors.Is(err, pro_interfaces.ErrOIDCGroupMappingForbidden):
		helpers.WriteErrorStatus(w, "OIDC_GROUP_MAPPING_FORBIDDEN", http.StatusForbidden)
	case errors.Is(err, pro_interfaces.ErrOIDCGroupMappingPreviewStale):
		helpers.WriteErrorStatus(w, "OIDC_GROUP_MAPPING_STALE", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrOIDCGroupMappingCollision):
		helpers.WriteErrorStatus(w, "OIDC_GROUP_MAPPING_COLLISION", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrOIDCGroupProtectedAdministrator):
		helpers.WriteErrorStatus(w, "OIDC_GROUP_PROTECTED_ADMINISTRATOR", http.StatusConflict)
	default:
		var validationErr *common_errors.ValidationError
		if errors.As(err, &validationErr) {
			helpers.WriteErrorStatus(w, validationErr.Error(), http.StatusBadRequest)
			return
		}
		log.WithError(err).Error("OIDC group mapping operation failed")
		helpers.WriteErrorStatus(w, "OIDC_GROUP_MAPPING_OPERATION_FAILED", http.StatusInternalServerError)
	}
}
