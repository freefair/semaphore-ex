package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"io"
	"net/http"
	"strings"
	"time"
)

type UserController struct {
}

// apiTokenResponse separates the public token reference from the credential.
// The credential is full only in the one-time create response; list responses
// retain the legacy abbreviated id field.
type apiTokenResponse struct {
	db.APIToken
	TokenRef string `json:"token_ref"`
}

func newAPITokenResponse(token db.APIToken, includeCredential bool) apiTokenResponse {
	response := apiTokenResponse{
		APIToken: token,
		TokenRef: token.StableID(),
	}
	if !includeCredential && len(response.ID) >= 8 {
		response.ID = response.ID[:8]
	}
	return response
}

func NewUserController() *UserController {
	return &UserController{}
}

func (c *UserController) GetUser(w http.ResponseWriter, r *http.Request) {
	if u, exists := helpers.GetOkFromContext(r, "_user"); exists {
		helpers.WriteJSON(w, http.StatusOK, u)
		return
	}

	var user struct {
		db.User
		CanCreateProject      bool `json:"can_create_project"`
		HasActiveSubscription bool `json:"has_active_subscription"`
	}

	user.User = *helpers.GetFromContext(r, "user").(*db.User)
	user.CanCreateProject = user.Admin || util.Config.NonAdminCanCreateProject
	user.HasActiveSubscription = false
	user.Pro = false
	helpers.WriteJSON(w, http.StatusOK, user)
}

// linkLdapIdentity attaches an LDAP identity to the current account.
// Proof of ownership is a successful bind with the user's own LDAP credentials.
func linkLdapIdentity(w http.ResponseWriter, r *http.Request) {
	linkLdapIdentityWithService(nil, nil, w, r)
	/* Legacy implementation is superseded by the Enhanced LDAP service path.
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

	provider, ok := util.Config.GetLdapProvider(providerID)
	if !ok {
		helpers.WriteErrorStatus(w, "LDAP provider not found", http.StatusBadRequest)
		return
	}

	ldapUser, userDN, err := tryFindLDAPUser(provider, creds.Username, creds.Password)
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
		helpers.Audit(r).Record(r.Context(), audit.Event{
			Kind:     audit.IAMExternalIdentityLink,
			Target:   audit.UserTarget(currentUser.ID, currentUser.Username),
			Metadata: audit.AuthMethodMetadata{Method: audit.LoginMethodLDAP, Provider: providerID},
		})
	}
	*/
}

func getAPITokens(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "user").(*db.User)

	tokens, err := helpers.Store(r).GetAPITokens(user.ID)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	responses := make([]apiTokenResponse, len(tokens))
	for i, token := range tokens {
		responses[i] = newAPITokenResponse(token, false)
	}

	helpers.WriteJSON(w, http.StatusOK, responses)
}

func createAPIToken(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "user").(*db.User)

	var body struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	if body.ExpiresAt != nil && !body.ExpiresAt.After(tz.Now()) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	tokenID := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenID); err != nil {
		panic(err)
	}

	token, err := helpers.Store(r).CreateAPIToken(db.APIToken{
		ID:        strings.ToLower(base64.URLEncoding.EncodeToString(tokenID)),
		UserID:    user.ID,
		Expired:   false,
		ExpiresAt: body.ExpiresAt,
		Name:      body.Name,
	})
	if err != nil {
		panic(err)
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:   audit.IAMAPITokenCreate,
		Target: &audit.Target{Type: audit.TargetAPIToken, ID: audit.TokenFingerprint(token.ID), Name: token.Name},
	})

	helpers.WriteJSON(w, http.StatusCreated, newAPITokenResponse(token, true))
}

func deleteAPIToken(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "user").(*db.User)

	tokenID := mux.Vars(r)["token_id"]
	if db.IsAPITokenStableID(tokenID) {
		tokens, err := helpers.Store(r).GetAPITokens(user.ID)
		if errors.Is(err, db.ErrNotFound) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		for _, token := range tokens {
			if token.StableID() != tokenID {
				continue
			}
			if err = helpers.Store(r).DeleteAPIToken(user.ID, tokenID); err != nil {
				helpers.WriteError(w, err)
				return
			}
			helpers.Audit(r).Record(r.Context(), audit.Event{
				Kind:   audit.IAMAPITokenDelete,
				Target: &audit.Target{Type: audit.TargetAPIToken, ID: audit.TokenFingerprint(token.ID), Name: token.Name},
			})
			break
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	tokens, err := helpers.Store(r).GetAPITokensByPrefix(user.ID, tokenID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	for _, token := range tokens {
		err = helpers.Store(r).DeleteAPIToken(user.ID, token.ID)
		if errors.Is(err, db.ErrNotFound) {
			continue
		}
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		helpers.Audit(r).Record(r.Context(), audit.Event{
			Kind:   audit.IAMAPITokenDelete,
			Target: &audit.Target{Type: audit.TargetAPIToken, ID: audit.TokenFingerprint(token.ID), Name: token.Name},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}
