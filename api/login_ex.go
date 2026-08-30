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
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	identityServices "github.com/semaphoreui/semaphore/services/identity"
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
	loginWithIdentityServices(totpService, nil, nil, w, r)
}

func loginWithIdentityServices(
	totpService pro_interfaces.TOTPService,
	ldapService pro_interfaces.LDAPService,
	audit pro_interfaces.AuditServiceFacade,
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method == "GET" {
		config := &loginMetadata{
			OidcProviders:     make([]loginMetadataOidcProvider, len(util.Config.OidcProviders)),
			LoginWithPassword: !util.Config.PasswordLoginDisable,
		}

		managedProviders, managed, managedErr := managedLDAPProviders(r.Context(), ldapService)
		if managedErr != nil {
			writeLDAPError(w, managedErr)
			return
		}
		if managed {
			config.LdapProviders = make([]loginMetadataLdapProvider, 0, len(managedProviders))
			for _, provider := range managedProviders {
				config.LdapProviders = append(config.LdapProviders, loginMetadataLdapProvider{
					ID: provider.ID, Name: provider.Name,
				})
			}
			config.LoginWithPassword = !util.Config.PasswordLoginDisable || len(managedProviders) > 0
			config.LocalRecoveryOnly = util.Config.PasswordLoginDisable && len(managedProviders) > 0
		} else {
			ldapProviders := util.Config.ActiveLdapProviders()
			config.LdapProviders = make([]loginMetadataLdapProvider, 0, len(ldapProviders))
			for _, entry := range ldapProviders {
				name := entry.Provider.DisplayName
				if name == "" {
					name = entry.ID
				}
				config.LdapProviders = append(config.LdapProviders, loginMetadataLdapProvider{
					ID: entry.ID, Name: name, Color: entry.Provider.Color, Icon: entry.Provider.Icon,
				})
			}
		}
		config.LoginWithLdap = len(config.LdapProviders) > 0

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
		allowed, allowErr := passwordLoginAllowed(r.Context(), ldapService, login.Auth)
		if allowErr != nil {
			writeLDAPError(w, allowErr)
			return
		}
		if !allowed {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		user, err = loginByPassword(helpers.Store(r), login.Auth, login.Password)

	case "ldap":
		providerID := login.Provider
		if providerID == "" {
			providerID = "ldap"
		}
		if ldapService != nil {
			user, err = ldapService.Authenticate(r.Context(), pro_interfaces.LDAPAuthenticationRequest{
				ProviderID: providerID, Username: login.Auth, Password: login.Password, Now: tz.Now(),
			})
			if !errors.Is(err, pro_interfaces.ErrLDAPUnavailable) {
				outcome, reason := ldapAuditReason(err)
				var actorID *int
				if err == nil {
					actorID = &user.ID
				}
				recordLDAPAudit(audit, r, actorID, pro_interfaces.AuditActionLDAPLogin, outcome, reason)
				if err != nil {
					writeLDAPError(w, err)
					return
				}
				break
			}
		}
		if _, ok := util.Config.GetLdapProvider(providerID); !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		user, err = loginByLegacyLDAP(r.Context(), helpers.Store(r), providerID, login.Auth, login.Password)

	default:
		_, managed, managedErr := managedLDAPProviders(r.Context(), ldapService)
		if managedErr != nil {
			writeLDAPError(w, managedErr)
			return
		}
		if managed {
			allowed, allowErr := passwordLoginAllowed(r.Context(), ldapService, login.Auth)
			if allowErr != nil {
				writeLDAPError(w, allowErr)
				return
			}
			if !allowed {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			user, err = loginByPassword(helpers.Store(r), login.Auth, login.Password)
		} else {
			user, err = loginLegacyCompatible(r.Context(), helpers.Store(r), login.Auth, login.Password)
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

func managedLDAPProviders(
	ctx context.Context,
	service pro_interfaces.LDAPService,
) ([]pro_interfaces.LDAPLoginProvider, bool, error) {
	if service == nil {
		return nil, false, nil
	}
	providers, err := service.LoginProviders(ctx)
	if errors.Is(err, pro_interfaces.ErrLDAPUnavailable) {
		return nil, false, nil
	}
	return providers, true, err
}

func passwordLoginAllowed(
	ctx context.Context,
	service pro_interfaces.LDAPService,
	login string,
) (bool, error) {
	if !util.Config.PasswordLoginDisable {
		return true, nil
	}
	if service == nil {
		return false, nil
	}
	allowed, err := service.AllowLocalRecovery(ctx, login)
	if errors.Is(err, pro_interfaces.ErrLDAPUnavailable) {
		return false, nil
	}
	return allowed, err
}

func loginByLegacyLDAP(
	ctx context.Context,
	store db.Store,
	providerID string,
	auth string,
	password string,
) (db.User, error) {
	ldapUser, externalID, err := authenticateLegacyLDAPProfile(ctx, providerID, auth, password)
	if err != nil || ldapUser == nil {
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"context": "ldap", "provider": providerID,
			}).Warn("Failed to authenticate against legacy LDAP provider")
		}
		return db.User{}, db.ErrNotFound
	}
	return loginByLDAP(store, *ldapUser, externalID, providerID)
}

func authenticateLegacyLDAPProfile(
	ctx context.Context,
	providerID string,
	auth string,
	password string,
) (*db.User, string, error) {
	provider, ok := util.Config.GetLdapProvider(providerID)
	if !ok {
		return nil, "", pro_interfaces.ErrLDAPProviderNotFound
	}
	mappings := provider.GetMappings()
	client := identityServices.NewLegacyLDAPClient()
	result, err := client.Authenticate(ctx, pro_interfaces.LegacyLDAPClientRequest{
		Configuration: pro_interfaces.LegacyLDAPClientConfiguration{
			Server: provider.Server, TLS: provider.NeedTLS, TLSSkipVerify: provider.TLSSkipVerify,
			BindDN: provider.BindDN, BindPassword: provider.BindPassword,
			SearchBaseDN: provider.SearchDN, SearchFilter: provider.SearchFilter,
			Attributes: []string{mappings.DN, mappings.Mail, mappings.UID, mappings.CN},
		},
		Username: auth, Credential: password,
	})
	if err != nil || result == nil {
		return nil, "", err
	}
	prepareClaims(result.Attributes)
	claims, err := parseClaims(result.Attributes, mappings)
	if err != nil {
		return nil, "", err
	}
	ldapUser := db.User{
		Username: strings.ToLower(claims.username), Created: tz.Now(), Name: claims.name,
		Email: claims.email, External: true,
	}
	if err = db.ValidateUser(ldapUser); err != nil {
		return nil, "", err
	}
	return &ldapUser, result.ExternalID, nil
}

func loginLegacyCompatible(
	ctx context.Context,
	store db.Store,
	auth string,
	password string,
) (db.User, error) {
	if _, ok := util.Config.GetLdapProvider("ldap"); ok {
		ldapUser, externalID, err := authenticateLegacyLDAPProfile(ctx, "ldap", auth, password)
		if err != nil {
			log.WithError(err).WithFields(log.Fields{
				"context": "ldap", "provider": "ldap",
			}).Warn("Failed to authenticate against legacy LDAP provider")
			return db.User{}, db.ErrNotFound
		}
		if ldapUser != nil {
			return loginByLDAP(store, *ldapUser, externalID, "ldap")
		}
	}
	if util.Config.PasswordLoginDisable {
		return db.User{}, db.ErrNotFound
	}
	return loginByPassword(store, auth, password)
}

func oidcGroupClaimConfiguration(provider util.OidcProvider) (pro_interfaces.OIDCGroupClaimConfiguration, bool, error) {
	if strings.TrimSpace(provider.GroupClaimPath) == "" {
		return pro_interfaces.OIDCGroupClaimConfiguration{}, false, nil
	}
	configuration := pro_interfaces.OIDCGroupClaimConfiguration{
		Path: provider.GroupClaimPath, CaseInsensitive: provider.GroupClaimCaseInsensitive,
		MissingClaimPolicy: pro_interfaces.OIDCMissingClaimPolicy(provider.GroupClaimMissingPolicy),
	}
	if err := pro_interfaces.NormalizeOIDCGroupClaimConfiguration(&configuration); err != nil {
		return pro_interfaces.OIDCGroupClaimConfiguration{}, false, err
	}
	return configuration, true, nil
}

func parseOIDCGroupClaim(
	claims map[string]any,
	provider util.OidcProvider,
) (*pro_interfaces.OIDCGroupClaimSet, error) {
	configuration, configured, err := oidcGroupClaimConfiguration(provider)
	if err != nil || !configured {
		return nil, err
	}
	groupClaim, err := pro_interfaces.ParseOIDCGroupClaim(claims, configuration)
	if err != nil {
		return nil, err
	}
	return &groupClaim, nil
}

func oidcRedirectWithTOTPService(
	totpService pro_interfaces.TOTPService,
	w http.ResponseWriter,
	r *http.Request,
) {
	oidcRedirectWithIdentityServices(totpService, nil, w, r)
}

func oidcRedirectWithIdentityServices(
	totpService pro_interfaces.TOTPService,
	oidcGroupMappingService pro_interfaces.OIDCGroupMappingService,
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
			claims, err = claimOidcToken(idToken, provider, !stateData.Link)
		}
	} else {
		var userInfo *oidc.UserInfo
		userInfo, err = _oidc.UserInfo(ctx, oauth2.StaticTokenSource(oauth2Token))

		if err == nil {
			if userInfo.Email == "" {
				claims, err = claimOidcUserInfo(userInfo, provider, !stateData.Link)
			} else {
				claims.email = userInfo.Email
				claims.name = userInfo.Profile
				claims.sub = userInfo.Subject
				claims.emailVerified = oidcEmailVerified(userInfo, provider)
				if !stateData.Link {
					var rawClaims map[string]any
					if err = userInfo.Claims(&rawClaims); err == nil {
						claims.groups, err = parseOIDCGroupClaim(rawClaims, provider)
					}
				}
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

	if claims.groups != nil && oidcGroupMappingService != nil {
		configuration, configured, configurationErr := oidcGroupClaimConfiguration(provider)
		if configurationErr != nil {
			log.WithError(configurationErr).WithFields(log.Fields{
				"context": "oidc_group_mapping", "provider": pid,
			}).Error("Invalid OIDC group mapping configuration")
			http.Error(w, "OIDC sign-in failed: invalid group mapping configuration. Contact your administrator.", http.StatusInternalServerError)
			return
		}
		if configured {
			_, reconciliationErr := oidcGroupMappingService.ReconcileGroupMappings(
				r.Context(), pro_interfaces.OIDCGroupPreviewRequest{
					ProviderID: pid, Configuration: configuration, Claim: *claims.groups,
					UserID: user.ID, Source: "login", Now: tz.Now(),
				})
			if reconciliationErr != nil && !errors.Is(reconciliationErr, pro_interfaces.ErrOIDCGroupMappingUnavailable) {
				// The verified identity remains usable when policy application cannot
				// complete. The service keeps the last known assignments and records
				// a bounded failure for the next login retry.
				log.WithError(reconciliationErr).WithFields(log.Fields{
					"context": "oidc_group_mapping", "provider": pid, "user_id": user.ID,
				}).Warn("OIDC group role reconciliation did not complete")
			}
		}
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
