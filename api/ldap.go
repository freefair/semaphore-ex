package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	log "github.com/sirupsen/logrus"
)

type LDAPController struct {
	service pro_interfaces.LDAPService
	audit   pro_interfaces.AuditServiceFacade
}

func NewLDAPController(
	service pro_interfaces.LDAPService,
	audit pro_interfaces.AuditServiceFacade,
) *LDAPController {
	return &LDAPController{service: service, audit: audit}
}

type ldapProviderBody struct {
	ID                string                       `json:"id" binding:"required"`
	DisplayName       string                       `json:"display_name" binding:"required"`
	ServerURL         string                       `json:"server_url" binding:"required"`
	TLSMode           pro_interfaces.LDAPTLSMode   `json:"tls_mode" binding:"required"`
	TrustMode         pro_interfaces.LDAPTrustMode `json:"trust_mode" binding:"required"`
	CAPEM             string                       `json:"ca_pem"`
	BindDN            string                       `json:"bind_dn" binding:"required"`
	BindPassword      string                       `json:"bind_password"`
	SearchBaseDN      string                       `json:"search_base_dn" binding:"required"`
	UserFilter        string                       `json:"user_filter" binding:"required"`
	IdentityAttribute string                       `json:"identity_attribute" binding:"required"`
	UsernameAttribute string                       `json:"username_attribute" binding:"required"`
	NameAttribute     string                       `json:"name_attribute" binding:"required"`
	EmailAttribute    string                       `json:"email_attribute" binding:"required"`
}

func (c *LDAPController) Providers(w http.ResponseWriter, r *http.Request) {
	providers, err := c.service.Providers(r.Context())
	if err != nil {
		writeLDAPError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, providers)
}

func (c *LDAPController) Configure(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body ldapProviderBody
	if !helpers.Bind(w, r, &body) {
		c.record(r, pro_interfaces.AuditActionLDAPConfigure,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	configuration, err := c.service.Configure(r.Context(), pro_interfaces.LDAPConfigureRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, Now: tz.Now(),
		Provider: pro_interfaces.LDAPProviderInput{
			ID: body.ID, DisplayName: body.DisplayName, ServerURL: body.ServerURL,
			TLSMode: body.TLSMode, TrustMode: body.TrustMode, CAPEM: body.CAPEM,
			BindDN: body.BindDN, BindPassword: body.BindPassword,
			SearchBaseDN: body.SearchBaseDN, UserFilter: body.UserFilter,
			IdentityAttribute: body.IdentityAttribute, UsernameAttribute: body.UsernameAttribute,
			NameAttribute: body.NameAttribute, EmailAttribute: body.EmailAttribute,
		},
	})
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionLDAPConfigure, err)
		writeLDAPError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionLDAPConfigure,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive))
	helpers.WriteJSON(w, http.StatusOK, configuration)
}

func (c *LDAPController) Test(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		ProviderID            string `json:"provider_id" binding:"required"`
		Username              string `json:"username" binding:"required"`
		Password              string `json:"password" binding:"required"`
		RecoveryAdminUserID   int    `json:"recovery_admin_user_id" binding:"required"`
		RecoveryAdminPassword string `json:"recovery_admin_password" binding:"required"`
	}
	if !helpers.Bind(w, r, &body) {
		c.record(r, pro_interfaces.AuditActionLDAPTest,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	readiness, err := c.service.Test(r.Context(), pro_interfaces.LDAPTestRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, ProviderID: body.ProviderID,
		Username: body.Username, Password: body.Password,
		RecoveryAdminUserID:   body.RecoveryAdminUserID,
		RecoveryAdminPassword: body.RecoveryAdminPassword, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionLDAPTest, err)
		writeLDAPError(w, err)
		return
	}
	c.record(r, pro_interfaces.AuditActionLDAPTest,
		pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive))
	helpers.WriteJSON(w, http.StatusOK, readiness)
}

func (c *LDAPController) SetState(w http.ResponseWriter, r *http.Request) {
	actor := helpers.GetFromContext(r, "user").(*db.User)
	var body struct {
		ProviderID      string                   `json:"provider_id" binding:"required"`
		State           pro_interfaces.LDAPState `json:"state" binding:"required"`
		SelectedUserIDs []int                    `json:"selected_user_ids"`
	}
	if !helpers.Bind(w, r, &body) {
		c.record(r, pro_interfaces.AuditActionLDAPConfigure,
			pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonInvalidInput)
		return
	}
	configuration, err := c.service.SetState(r.Context(), pro_interfaces.LDAPStateRequest{
		ActorID: actor.ID, ActorIsAdmin: actor.Admin, ProviderID: body.ProviderID,
		State: body.State, SelectedUserIDs: body.SelectedUserIDs, Now: tz.Now(),
	})
	if err != nil {
		c.recordError(r, pro_interfaces.AuditActionLDAPConfigure, err)
		writeLDAPError(w, err)
		return
	}
	reason := string(pro_interfaces.CapabilityReasonActive)
	if body.State == pro_interfaces.LDAPStateDisabled {
		reason = string(pro_interfaces.CapabilityReasonDisabledByAdmin)
	} else if body.State == pro_interfaces.LDAPStateShadow {
		reason = string(pro_interfaces.CapabilityReasonShadow)
	}
	c.record(r, pro_interfaces.AuditActionLDAPConfigure, pro_interfaces.AuditOutcomeAllowed, reason)
	helpers.WriteJSON(w, http.StatusOK, configuration)
}

func (c *LDAPController) Transitions(w http.ResponseWriter, r *http.Request) {
	providerID := strings.TrimSpace(r.URL.Query().Get("provider_id"))
	if providerID == "" {
		helpers.WriteErrorStatus(w, "LDAP_PROVIDER_ID_REQUIRED", http.StatusBadRequest)
		return
	}
	transitions, err := c.service.Transitions(r.Context(), providerID)
	if err != nil {
		writeLDAPError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, transitions)
}

func (c *LDAPController) recordError(
	r *http.Request,
	action pro_interfaces.AuditAction,
	err error,
) {
	outcome := pro_interfaces.AuditOutcomeFailure
	reason := pro_interfaces.AuditReasonOperationError
	switch {
	case errors.Is(err, pro_interfaces.ErrLDAPInvalidCredentials):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonLDAPInvalidCredentials
	case errors.Is(err, pro_interfaces.ErrLDAPThrottled):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonThrottled
	case errors.Is(err, pro_interfaces.ErrLDAPForbidden),
		errors.Is(err, pro_interfaces.ErrLDAPDisabled),
		errors.Is(err, pro_interfaces.ErrLDAPReconfigurationRequiresInactive),
		errors.Is(err, pro_interfaces.ErrLDAPIdentityCollision):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonLDAPPolicy
	case errors.Is(err, pro_interfaces.ErrLDAPReadiness):
		outcome = pro_interfaces.AuditOutcomeDenied
		reason = pro_interfaces.AuditReasonReadiness
	case errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable),
		errors.Is(err, pro_interfaces.ErrLDAPReferral),
		errors.Is(err, pro_interfaces.ErrLDAPDuplicateIdentity):
		reason = pro_interfaces.AuditReasonProviderError
	}
	c.record(r, action, outcome, reason)
}

func (c *LDAPController) record(
	r *http.Request,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
) {
	recordLDAPAudit(c.audit, r, nil, action, outcome, reason)
}

func recordLDAPAudit(
	audit pro_interfaces.AuditServiceFacade,
	r *http.Request,
	actorID *int,
	action pro_interfaces.AuditAction,
	outcome pro_interfaces.AuditOutcome,
	reason string,
) {
	if audit == nil {
		return
	}
	event := capabilityAuditEvent(r, action, outcome, reason)
	event.TargetID = string(pro_interfaces.CapabilityLDAP)
	if actorID != nil {
		event.ActorID = actorID
	}
	if err := audit.Record(r.Context(), event); err != nil {
		log.WithFields(event.SafeFields()).Error("Failed to store LDAP security audit event")
	}
}

func ldapAuditReason(err error) (pro_interfaces.AuditOutcome, string) {
	switch {
	case err == nil:
		return pro_interfaces.AuditOutcomeAllowed, string(pro_interfaces.CapabilityReasonActive)
	case errors.Is(err, pro_interfaces.ErrLDAPInvalidCredentials):
		return pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonLDAPInvalidCredentials
	case errors.Is(err, pro_interfaces.ErrLDAPThrottled):
		return pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonThrottled
	case errors.Is(err, pro_interfaces.ErrLDAPForbidden),
		errors.Is(err, pro_interfaces.ErrLDAPDisabled),
		errors.Is(err, pro_interfaces.ErrLDAPIdentityCollision):
		return pro_interfaces.AuditOutcomeDenied, pro_interfaces.AuditReasonLDAPPolicy
	case errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable),
		errors.Is(err, pro_interfaces.ErrLDAPReferral),
		errors.Is(err, pro_interfaces.ErrLDAPDuplicateIdentity):
		return pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonProviderError
	default:
		return pro_interfaces.AuditOutcomeFailure, pro_interfaces.AuditReasonOperationError
	}
}

func writeLDAPError(w http.ResponseWriter, err error) {
	var validationError *common_errors.ValidationError
	switch {
	case errors.As(err, &validationError):
		helpers.WriteErrorStatus(w, validationError.Error(), http.StatusBadRequest)
	case errors.Is(err, pro_interfaces.ErrLDAPInvalidCredentials):
		helpers.WriteErrorStatus(w, "LDAP_INVALID_CREDENTIALS", http.StatusUnauthorized)
	case errors.Is(err, pro_interfaces.ErrLDAPProviderUnavailable),
		errors.Is(err, pro_interfaces.ErrLDAPReferral),
		errors.Is(err, pro_interfaces.ErrLDAPDuplicateIdentity):
		helpers.WriteErrorStatus(w, "LDAP_PROVIDER_UNAVAILABLE", http.StatusServiceUnavailable)
	case errors.Is(err, pro_interfaces.ErrLDAPThrottled):
		w.Header().Set("Retry-After", "300")
		helpers.WriteErrorStatus(w, "LDAP_THROTTLED", http.StatusTooManyRequests)
	case errors.Is(err, pro_interfaces.ErrLDAPIdentityCollision):
		helpers.WriteErrorStatus(w, "LDAP_IDENTITY_COLLISION", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrLDAPReconfigurationRequiresInactive):
		helpers.WriteErrorStatus(w, "LDAP_RECONFIGURATION_REQUIRES_INACTIVE", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrLDAPReadiness):
		helpers.WriteErrorStatus(w, "LDAP_ADMIN_RECOVERY_NOT_READY", http.StatusConflict)
	case errors.Is(err, pro_interfaces.ErrLDAPDisabled):
		helpers.WriteErrorStatus(w, "LDAP_DISABLED", http.StatusForbidden)
	case errors.Is(err, pro_interfaces.ErrLDAPForbidden):
		helpers.WriteErrorStatus(w, "LDAP_FORBIDDEN", http.StatusForbidden)
	case errors.Is(err, pro_interfaces.ErrLDAPProviderNotFound), errors.Is(err, db.ErrNotFound):
		helpers.WriteErrorStatus(w, "LDAP_PROVIDER_NOT_FOUND", http.StatusNotFound)
	case errors.Is(err, pro_interfaces.ErrLDAPUnavailable):
		helpers.WriteErrorStatus(w, "LDAP_UNAVAILABLE", http.StatusForbidden)
	default:
		log.WithError(err).Error("LDAP operation failed")
		helpers.WriteErrorStatus(w, "LDAP_OPERATION_FAILED", http.StatusInternalServerError)
	}
}
