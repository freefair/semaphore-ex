package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func loginWithTOTPService(
	totpService pro_interfaces.TOTPService,
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method == "GET" {
		config := &loginMetadata{
			OidcProviders:     make([]loginMetadataOidcProvider, len(util.Config.OidcProviders)),
			LoginWithPassword: !util.Config.PasswordLoginDisable,
		}

		ldapProviders := util.Config.ActiveLdapProviders()
		config.LdapProviders = make([]loginMetadataLdapProvider, 0, len(ldapProviders))
		for _, entry := range ldapProviders {
			name := entry.Provider.DisplayName
			if name == "" {
				name = entry.ID
			}
			config.LdapProviders = append(config.LdapProviders, loginMetadataLdapProvider{
				ID:    entry.ID,
				Name:  name,
				Color: entry.Provider.Color,
				Icon:  entry.Provider.Icon,
			})
		}
		config.LoginWithLdap = len(ldapProviders) > 0

		i := 0

		for k, v := range util.Config.OidcProviders {
			config.OidcProviders[i] = loginMetadataOidcProvider{
				ID:    k,
				Name:  v.DisplayName,
				Color: v.Color,
				Icon:  v.Icon,
			}
			i++
		}

		sort.Slice(config.OidcProviders, func(i, j int) bool {
			a := util.Config.OidcProviders[config.OidcProviders[i].ID]
			b := util.Config.OidcProviders[config.OidcProviders[j].ID]
			return a.Order < b.Order
		})

		if totpService != nil {
			status, statusErr := totpService.Status(r.Context(), 0)
			if statusErr == nil && status.CapabilityState != pro_interfaces.CapabilityStateDisabled &&
				status.CapabilityState != pro_interfaces.CapabilityStateShadow {
				config.AuthMethods.Totp = &LoginTotpAuthMethod{AllowRecovery: true}
			}
		} else if util.Config.Mfa.Totp.Enabled {
			config.AuthMethods.Totp = &LoginTotpAuthMethod{
				AllowRecovery: util.Config.Mfa.Totp.AllowRecovery,
			}
		}

		helpers.WriteJSON(w, http.StatusOK, config)
		return
	}

	var login struct {
		Auth     string `json:"auth" binding:"required"`
		Password string `json:"password" binding:"required"`
		Method   string `json:"method"`   // "", "password" or "ldap"
		Provider string `json:"provider"` // LDAP provider ID when method == "ldap"
	}
	if !helpers.Bind(w, r, &login) {
		return
	}

	login.Auth = strings.ToLower(login.Auth)

	var err error
	var user db.User

	switch login.Method {
	case "password":
		if util.Config.PasswordLoginDisable {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		user, err = loginByPassword(helpers.Store(r), login.Auth, login.Password)

	case "ldap":
		providerID := login.Provider
		if providerID == "" {
			providerID = "ldap"
		}
		provider, ok := util.Config.GetLdapProvider(providerID)
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var ldapUser *db.User
		var ldapUserDN string
		ldapUser, ldapUserDN, err = tryFindLDAPUser(provider, login.Auth, login.Password)
		if err != nil || ldapUser == nil {
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"context":  "ldap",
					"provider": providerID,
					"auth":     login.Auth,
				}).Warn("Failed to find user in LDAP")
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		user, err = loginByLDAP(helpers.Store(r), *ldapUser, ldapUserDN, providerID)

	default:
		// Legacy clients without the method field: previous behavior —
		// try the legacy flat LDAP first, fall back to password.
		var ldapUser *db.User
		var ldapUserDN string

		if legacy, ok := util.Config.GetLdapProvider("ldap"); ok {
			ldapUser, ldapUserDN, err = tryFindLDAPUser(legacy, login.Auth, login.Password)
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"context": "ldap",
					"auth":    login.Auth,
				}).Warn("Failed to find user in LDAP")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
		}

		if ldapUser == nil {
			if util.Config.PasswordLoginDisable {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			user, err = loginByPassword(helpers.Store(r), login.Auth, login.Password)
		} else {
			user, err = loginByLDAP(helpers.Store(r), *ldapUser, ldapUserDN, "ldap")
		}
	}

	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var validationError *common_errors.ValidationError
		switch {
		case errors.As(err, &validationError):
			// TODO: Return more informative error code.
		}

		log.Error(err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if !createSession(w, r, user, false, totpService) {
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func oidcRedirectWithTOTPService(
	totpService pro_interfaces.TOTPService,
	w http.ResponseWriter,
	r *http.Request,
) {
	pid := mux.Vars(r)["provider"]
	oauthState, err := r.Cookie("oauthstate")

	// Errors are shown as plain text at the current URL instead of a silent
	// redirect to the login page, so the user can see what went wrong.
	// Details stay in server logs.

	if err != nil {
		log.Error(err.Error())
		http.Error(w, "OIDC sign-in failed: state cookie is missing. Try signing in again.", http.StatusBadRequest)
		return
	}

	s := r.FormValue("state")
	b, err := base64.URLEncoding.DecodeString(s)

	if err != nil {
		log.Error(err.Error())
		http.Error(w, "OIDC sign-in failed: invalid state. Try signing in again.", http.StatusBadRequest)
		return
	}

	var stateData oAuthState
	err = json.Unmarshal(b, &stateData)

	if err != nil {
		log.Error(err.Error())
		http.Error(w, "OIDC sign-in failed: invalid state. Try signing in again.", http.StatusBadRequest)
		return
	}

	if stateData.Csrf != oauthState.Value {
		http.Error(w, "OIDC sign-in failed: state mismatch. Try signing in again.", http.StatusBadRequest)
		return
	}

	ctx := context.Background()

	_oidc, oauth, err := getOidcProvider(pid, ctx, r.URL.Path)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, "Failed to initialize OIDC provider. Contact your administrator.", http.StatusInternalServerError)
		return
	}

	provider, ok := util.Config.OidcProviders[pid]
	if !ok {
		log.Error(fmt.Errorf("no such provider: %s", pid))
		http.Error(w, "Unknown OIDC provider.", http.StatusNotFound)
		return
	}

	verifier := _oidc.Verifier(&oidc.Config{ClientID: oauth.ClientID})

	code := r.URL.Query().Get("code")

	oauth2Token, err := oauth.Exchange(ctx, code)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, "OIDC sign-in failed: could not exchange authorization code. Contact your administrator.", http.StatusUnauthorized)
		return
	}

	var claims claimResult

	// Extract the ID Token from OAuth2 token.
	rawIDToken, ok := oauth2Token.Extra("id_token").(string)

	if ok && rawIDToken != "" {
		var idToken *oidc.IDToken
		// Parse and verify ID Token payload.
		idToken, err = verifier.Verify(ctx, rawIDToken)

		if err == nil {
			claims, err = claimOidcToken(idToken, provider)
		}
	} else {
		var userInfo *oidc.UserInfo
		userInfo, err = _oidc.UserInfo(ctx, oauth2.StaticTokenSource(oauth2Token))

		if err == nil {
			if userInfo.Email == "" {
				claims, err = claimOidcUserInfo(userInfo, provider)
			} else {
				claims.email = userInfo.Email
				claims.name = userInfo.Profile
				claims.sub = userInfo.Subject
				claims.emailVerified = oidcEmailVerified(userInfo, provider)
			}
		}

		claims.username = getRandomUsername()
		if userInfo.Profile == "" {
			claims.name = getRandomProfileName()
		}
	}

	if err != nil {
		log.Error(err.Error())
		http.Error(w, "OIDC sign-in failed: could not read user info from the provider. Contact your administrator.", http.StatusBadGateway)
		return
	}

	if claims.sub == "" {
		log.Error(fmt.Errorf("oidc provider %s returned no sub claim", pid))
		http.Error(w, "OIDC sign-in failed: the provider returned no user ID (sub claim). Contact your administrator.", http.StatusBadGateway)
		return
	}

	if stateData.Link {
		session, ok := getSession(r)
		if !ok || !session.IsVerified() {
			http.Error(w, "You must be signed in to link an external account.", http.StatusUnauthorized)
			return
		}

		sessionUser, uErr := helpers.Store(r).GetUser(session.UserID)
		if uErr != nil {
			log.Error(uErr.Error())
			http.Error(w, "Failed to link external account.", http.StatusInternalServerError)
			return
		}

		if lErr := linkExternalIdentity(helpers.Store(r), sessionUser, db.IdentityTypeOidc, pid, claims.sub); lErr != nil {
			log.WithError(lErr).WithFields(log.Fields{
				"user_id":  sessionUser.ID,
				"provider": pid,
				"context":  "oidc_link",
			}).Error("Failed to link external identity")

			switch {
			case errors.Is(lErr, errIdentityLinkedToAnother):
				http.Error(w, "This external account is already linked to another user.", http.StatusConflict)
			case errors.Is(lErr, errProviderAlreadyLinked):
				http.Error(w, "Your account already has a linked identity for this provider. Unlink it first.", http.StatusConflict)
			default:
				http.Error(w, "Failed to link external account.", http.StatusInternalServerError)
			}
			return
		}

		redirectURL, _ := url.JoinPath(util.Config.WebHost, "/")
		http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
		return
	}

	user, err := resolveExternalUser(helpers.Store(r), externalUserProfile{
		Type:          db.IdentityTypeOidc,
		Provider:      pid,
		ExternalUID:   claims.sub,
		Username:      claims.username,
		Name:          claims.name,
		Email:         claims.email,
		EmailVerified: claims.emailVerified,
		// MatchByUsername stays false: OIDC matches by email only
		// (username matching "creates a lot of problems" - see old comment).
	})
	if err != nil {
		log.Error(err.Error())
		http.Error(w, "OIDC sign-in failed: could not find or create the user account. Contact your administrator.", http.StatusInternalServerError)
		return
	}

	if !createSession(w, r, user, true, totpService) {
		return
	}

	config, ok := util.Config.OidcProviders[pid]
	if !ok {
		log.Error(fmt.Errorf("no such provider: %s", pid))
		http.Error(w, "Unknown OIDC provider.", http.StatusNotFound)
		return
	}

	redirectPath := ""
	if config.ReturnViaState {
		redirectPath = stateData.Return
	} else {
		redirectPath = mux.Vars(r)["redirect_path"]
	}

	redirectURL, err := oidcSuccessRedirectURL(util.Config.WebHost, redirectPath)
	if err != nil {
		log.Error(err)
		http.Error(w, "OIDC sign-in failed: invalid redirect URL.", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, redirectURL, http.StatusTemporaryRedirect)
}
