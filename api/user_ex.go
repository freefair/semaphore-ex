package api

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	log "github.com/sirupsen/logrus"
	"net/http"
)

func linkLdapIdentityWithService(
	service pro_interfaces.LDAPService,
	auditFacade pro_interfaces.AuditServiceFacade,
	w http.ResponseWriter,
	r *http.Request,
) {
	currentUser := helpers.GetFromContext(r, "user").(*db.User)

	var creds struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
		Provider string `json:"provider"` // LDAP provider ID, default "ldap"
	}
	if !helpers.Bind(w, r, &creds) {
		return
	}

	providerID := creds.Provider
	if providerID == "" {
		providerID = "ldap"
	}
	if service != nil {
		linked, err := service.Link(r.Context(), pro_interfaces.LDAPLinkRequest{
			ActorID: currentUser.ID, ProviderID: providerID,
			Username: creds.Username, Password: creds.Password, Now: tz.Now(),
		})
		if !errors.Is(err, pro_interfaces.ErrLDAPUnavailable) {
			outcome, reason := ldapAuditReason(err)
			recordLDAPAudit(auditFacade, r, &currentUser.ID,
				pro_interfaces.AuditActionLDAPLink, outcome, reason)
			if err != nil {
				writeLDAPError(w, err)
				return
			}
			if linked {
				recordExternalResolution(r, *currentUser, resolvedLinked,
					audit.AuthMethodMetadata{Method: audit.LoginMethodLDAP, Provider: providerID})
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	ldapUser, userDN, err := authenticateLegacyLDAPProfile(
		r.Context(), providerID, creds.Username, creds.Password,
	)
	if errors.Is(err, pro_interfaces.ErrLDAPProviderNotFound) {
		helpers.WriteErrorStatus(w, "LDAP provider not found", http.StatusBadRequest)
		return
	}
	if err != nil || ldapUser == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !ldapProfileMatchesSemaphoreUser(*ldapUser, *currentUser) {
		helpers.WriteErrorStatus(w, "LDAP directory profile does not match your account", http.StatusForbidden)
		return
	}

	linked, err := linkExternalIdentity(helpers.Store(r), *currentUser, db.IdentityTypeLdap, providerID, userDN)
	if err != nil {
		switch {
		case errors.Is(err, errIdentityLinkedToAnother):
			helpers.WriteErrorStatus(w, "This LDAP account is already linked to another user.", http.StatusConflict)
		case errors.Is(err, errProviderAlreadyLinked):
			helpers.WriteErrorStatus(w, "Your account already has a linked LDAP identity. Unlink it first.", http.StatusConflict)
		default:
			log.WithError(err).WithFields(log.Fields{
				"provider": providerID,
				"user_dn":  userDN,
				"context":  "ldap",
			}).Warn("Failed to link LDAP identity")
			helpers.WriteErrorStatus(w, "Failed to link LDAP account", http.StatusInternalServerError)
		}
		return
	}
	if linked {
		recordExternalResolution(r, *currentUser, resolvedLinked,
			audit.AuthMethodMetadata{Method: audit.LoginMethodLDAP, Provider: providerID})
	}

	w.WriteHeader(http.StatusNoContent)
}
