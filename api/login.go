package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-ldap/ldap/v3"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/random"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/template"
	"time"
)

func convertEntryToMap(entity *ldap.Entry) map[string]any {
	res := map[string]any{}
	for _, attr := range entity.Attributes {
		if len(attr.Values) == 0 {
			continue
		}
		res[attr.Name] = attr.Values[0]
	}

	return res
}

func tryFindLDAPUser(provider util.LdapProvider, username, password string) (*db.User, string, error) {
	var l *ldap.Conn
	var err error
	if provider.NeedTLS {
		// Verify the LDAP server certificate by default so a network attacker
		// cannot impersonate the server to capture the bind credentials or a
		// user's cleartext password. Verification can be disabled per provider
		// via tls_skip_verify (default false) for trusted networks with
		// self-signed certificates.
		l, err = ldap.DialTLS("tcp", provider.Server, &tls.Config{
			InsecureSkipVerify: provider.TLSSkipVerify, //nolint:gosec // opt-in via tls_skip_verify, defaults to false
		})
	} else {
		l, err = ldap.Dial("tcp", provider.Server)
	}

	if err != nil {
		return nil, "", err
	}
	defer l.Close() //nolint:errcheck

	// First bind with a read only user
	if err = l.Bind(provider.BindDN, provider.BindPassword); err != nil {
		return nil, "", err
	}

	mappings := provider.GetMappings()

	// Filter for the given username
	searchRequest := ldap.NewSearchRequest(
		provider.SearchDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(provider.SearchFilter, ldap.EscapeFilter(username)),
		[]string{mappings.DN},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		return nil, "", err
	}

	if len(sr.Entries) < 1 {
		return nil, "", nil
	}

	if len(sr.Entries) > 1 {
		return nil, "", fmt.Errorf("too many entries returned")
	}

	// Bind as the user
	userDN := sr.Entries[0].DN
	if err = l.Bind(userDN, password); err != nil {
		return nil, "", err
	}

	// Second time bind as read only user
	if err = l.Bind(provider.BindDN, provider.BindPassword); err != nil {
		return nil, "", err
	}

	// Get user info
	searchRequest = ldap.NewSearchRequest(
		provider.SearchDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf(provider.SearchFilter, ldap.EscapeFilter(username)),
		[]string{mappings.DN, mappings.Mail, mappings.UID, mappings.CN},
		nil,
	)

	sr, err = l.Search(searchRequest)
	if err != nil {
		return nil, "", err
	}

	if len(sr.Entries) <= 0 {
		return nil, "", fmt.Errorf("ldap search returned no entries")
	}

	entry := convertEntryToMap(sr.Entries[0])

	prepareClaims(entry)

	claims, err := parseClaims(entry, mappings)
	if err != nil {
		return nil, "", err
	}

	ldapUser := db.User{
		Username: strings.ToLower(claims.username),
		Created:  tz.Now(),
		Name:     claims.name,
		Email:    claims.email,
		External: true,
		Alert:    false,
	}

	err = db.ValidateUser(ldapUser)
	if err != nil {
		jsonBytes, _ := json.Marshal(ldapUser)
		log.Error("LDAP returned incorrect user data: " + string(jsonBytes))
		return nil, "", err
	}

	log.Info("User " + ldapUser.Name + " with email " + ldapUser.Email + " authorized via LDAP correctly")
	return &ldapUser, userDN, nil
}

// createSession creates session for passed user and stores session details
// in cookies.
func createSession(
	w http.ResponseWriter,
	r *http.Request,
	user db.User,
	oidc bool,
	totpService pro_interfaces.TOTPService,
) bool {
	var err error
	var verificationMethod db.SessionVerificationMethod
	verified := false

	if totpService != nil {
		requirement, requirementErr := totpService.SessionRequirement(r.Context(), user.ID)
		if requirementErr != nil {
			log.WithError(requirementErr).WithField("user_id", user.ID).Error("Failed to resolve TOTP session requirement")
			if errors.Is(requirementErr, pro_interfaces.ErrTOTPUnavailable) {
				helpers.WriteErrorStatus(w, "TOTP_UNAVAILABLE", http.StatusForbidden)
				return false
			}
			if errors.Is(requirementErr, pro_interfaces.ErrTOTPForbidden) {
				helpers.WriteErrorStatus(w, "TOTP_POLICY_UNSATISFIED", http.StatusForbidden)
				return false
			}
			helpers.WriteErrorStatus(w, "Failed to resolve authentication policy", http.StatusInternalServerError)
			return false
		}
		switch requirement {
		case pro_interfaces.TOTPSessionChallenge:
			verificationMethod = db.SessionVerificationTotp
		case pro_interfaces.TOTPSessionEnroll:
			verificationMethod = db.SessionVerificationTotpEnrollment
		default:
			verificationMethod = db.SessionVerificationNone
			verified = true
		}
	} else {
		switch {
		case user.Totp != nil && util.Config.Mfa.Totp.Enabled:
			verificationMethod = db.SessionVerificationTotp
		default:
			verificationMethod = db.SessionVerificationNone
			verified = true
		}
	}

	newSession, err := helpers.Store(r).CreateSession(db.Session{
		UserID:             user.ID,
		Created:            tz.Now(),
		LastActive:         tz.Now(),
		IP:                 r.Header.Get("X-Real-IP"),
		UserAgent:          r.Header.Get("user-agent"),
		Expired:            false,
		VerificationMethod: verificationMethod,
		Verified:           verified,
	})

	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"user_id": user.ID,
			"context": "session",
		}).Error("Failed to create session")
		helpers.WriteErrorStatus(w, "Failed to create session", http.StatusInternalServerError)
		return false
	}

	encoded, err := util.Cookie.Encode("semaphore", map[string]any{
		"user":    user.ID,
		"session": newSession.ID,
	})
	if err != nil {
		log.WithError(err).WithFields(log.Fields{
			"user_id": user.ID,
			"context": "session",
		}).Error("Failed to encode session cookie")
		helpers.WriteErrorStatus(w, "Failed to create session", http.StatusInternalServerError)
		return false
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "semaphore",
		Value:    encoded,
		Path:     "/",
		HttpOnly: true,
		// SameSite=Lax prevents the session cookie from being attached to
		// cross-site POST requests, which mitigates CSRF (e.g. the password
		// change endpoint). Top-level GET navigations still carry the cookie
		// so following a link into Semaphore keeps the user logged in.
		SameSite: http.SameSiteLaxMode,
		// Secure is only enforced when Semaphore is served over HTTPS, so that
		// it can still be used without TLS inside private networks.
		Secure: isSecureWebHost(),
	})
	return true
}

// isSecureWebHost reports whether Semaphore's public web host uses HTTPS, in
// which case cookies should carry the Secure attribute.
func isSecureWebHost() bool {
	return util.WebHostURL != nil && util.WebHostURL.Scheme == "https"
}

func loginByPassword(store db.Store, login string, password string) (user db.User, err error) {
	user, err = store.GetUserByLoginOrEmail(login, login)
	if err != nil {
		return
	}

	if user.External {
		err = db.ErrNotFound
		return
	}

	err = bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		err = db.ErrNotFound
		return
	}

	return
}

func loginByLDAP(store db.Store, ldapUser db.User, userDN string, providerID string) (db.User, error) {
	return resolveExternalUser(store, externalUserProfile{
		Type:            db.IdentityTypeLdap,
		Provider:        providerID,
		ExternalUID:     userDN,
		Username:        ldapUser.Username,
		Name:            ldapUser.Name,
		Email:           ldapUser.Email,
		MatchByUsername: true,
		// The email comes from the directory, not the user - authoritative.
		EmailVerified: true,
	})
}

type loginMetadataOidcProvider struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Icon  string `json:"icon"`
}

type LoginTotpAuthMethod struct {
	AllowRecovery bool `json:"allow_recovery"`
}

type LoginEmailAuthMethod struct {
}

type LoginAuthMethods struct {
	Totp  *LoginTotpAuthMethod  `json:"totp,omitempty"`
	Email *LoginEmailAuthMethod `json:"email,omitempty"`
}

type loginMetadataLdapProvider struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

type loginMetadata struct {
	OidcProviders     []loginMetadataOidcProvider `json:"oidc_providers"`
	LdapProviders     []loginMetadataLdapProvider `json:"ldap_providers"`
	LoginWithPassword bool                        `json:"login_with_password"`
	LoginWithLdap     bool                        `json:"login_with_ldap"`
	AuthMethods       LoginAuthMethods            `json:"auth_methods"`
}

// nolint: gocyclo
func login(w http.ResponseWriter, r *http.Request) {
	loginWithTOTPService(nil, w, r)
}

// logout handles the user logout process by expiring the current session
// and clearing the session cookie.
//
// Behavior:
//   - If a valid session exists, it is expired in the database.
//   - The session cookie is cleared by setting its value to an empty string
//     and its expiration date to a past time.
//
// Responses:
// - 204 No Content: Logout successful.
// - 500 Internal Server Error: An error occurred while expiring the session.
func logout(w http.ResponseWriter, r *http.Request) {
	if session, ok := getSession(r); ok {
		err := helpers.Store(r).ExpireSession(session.UserID, session.ID)
		if err != nil {
			log.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "semaphore",
		Value:    "",
		Expires:  tz.Now().Add(24 * 7 * time.Hour * -1),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isSecureWebHost(),
	})

	w.WriteHeader(http.StatusNoContent)
}

func getOidcProvider(id string, ctx context.Context, redirectPath string) (*oidc.Provider, *oauth2.Config, error) {
	provider, ok := util.Config.OidcProviders[id]
	if !ok {
		return nil, nil, fmt.Errorf("no such provider: %s", id)
	}
	config := oidc.ProviderConfig{
		IssuerURL:   provider.Endpoint.IssuerURL,
		AuthURL:     provider.Endpoint.AuthURL,
		TokenURL:    provider.Endpoint.TokenURL,
		UserInfoURL: provider.Endpoint.UserInfoURL,
		JWKSURL:     provider.Endpoint.JWKSURL,
		Algorithms:  provider.Endpoint.Algorithms,
	}
	oidcProvider := config.NewProvider(ctx)
	var err error
	if provider.AutoDiscovery != "" {
		oidcProvider, err = oidc.NewProvider(ctx, provider.AutoDiscovery)
		if err != nil {
			return nil, nil, err
		}
	}

	clientID := provider.ClientID
	if provider.ClientIDFile != "" {
		if clientID, err = getSecretFromFile(provider.ClientIDFile); err != nil {
			return nil, nil, err
		}
	}

	clientSecret := provider.ClientSecret
	if provider.ClientSecretFile != "" {
		if clientSecret, err = getSecretFromFile(provider.ClientSecretFile); err != nil {
			return nil, nil, err
		}
	}

	if redirectPath != "" {
		redirectPath = strings.TrimRight(redirectPath, "/")

		providerUrl, err2 := url.Parse(provider.RedirectURL)

		if err2 != nil {
			return nil, nil, err2
		}

		providerPath := strings.TrimRight(providerUrl.Path, "/")

		if redirectPath == providerPath {
			redirectPath = ""
		} else if strings.HasPrefix(redirectPath, providerPath+"/") {
			redirectPath = redirectPath[len(providerPath):]
		}
	}

	oauthConfig := oauth2.Config{
		Endpoint:     oidcProvider.Endpoint(),
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  provider.RedirectURL + redirectPath,
		Scopes:       provider.Scopes,
	}
	if len(oauthConfig.RedirectURL) == 0 {
		redirectURL, err := url.JoinPath(util.Config.WebHost, "api/auth/oidc", id, "redirect")
		if err != nil {
			return nil, nil, err
		}

		oauthConfig.RedirectURL = redirectURL

		if redirectURL != redirectPath {
			oauthConfig.RedirectURL += redirectPath
		}
	}
	if len(oauthConfig.Scopes) == 0 {
		oauthConfig.Scopes = []string{"openid", "profile", "email"}
	}
	return oidcProvider, &oauthConfig, nil
}

func oidcLogin(w http.ResponseWriter, r *http.Request) {
	pid := mux.Vars(r)["provider"]
	ctx := context.Background()

	returnPath := ""
	redirectPath := ""

	config, ok := util.Config.OidcProviders[pid]
	if !ok {
		log.Error(fmt.Errorf("no such provider: %s", pid))
		http.Error(w, "Unknown OIDC provider.", http.StatusNotFound)
		return
	}

	linkMode := r.URL.Query().Get("link") != ""

	if linkMode {
		// POST-only: SameSite=Lax attaches the session cookie to top-level
		// cross-site GET navigations, so a GET here would let an attacker
		// initiate linking (CSRF) and attach their IdP identity to the
		// victim's account. Lax never sends the cookie on cross-site POST.
		if r.Method != http.MethodPost {
			http.Error(w, "Account linking must be initiated with a POST request.", http.StatusMethodNotAllowed)
			return
		}
		session, ok := getSession(r)
		if !ok || !session.IsVerified() {
			http.Error(w, "You must be signed in to link an external account.", http.StatusUnauthorized)
			return
		}
	}

	returnValue := r.URL.Query().Get("return")
	if returnValue != "" {
		if config.ReturnViaState {
			returnPath = returnValue
		} else {
			redirectPath = returnValue
		}
	}

	_, oauth, err := getOidcProvider(pid, ctx, redirectPath)
	if err != nil {
		log.Error(err.Error())
		http.Error(w, "Failed to initialize OIDC provider. Contact your administrator.", http.StatusInternalServerError)
		return
	}
	state := generateStateOauthCookie(w, returnPath, linkMode)
	u := oauth.AuthCodeURL(state)
	status := http.StatusTemporaryRedirect
	if r.Method == http.MethodPost {
		// 303 turns the form POST into a GET on the IdP authorize URL.
		status = http.StatusSeeOther
	}
	http.Redirect(w, r, u, status)
}

type oAuthState struct {
	Csrf   string `json:"csrf"`
	Return string `json:"return"`
	Link   bool   `json:"link,omitempty"`
}

func generateStateOauthCookie(w http.ResponseWriter, returnPath string, link bool) string {

	expiration := tz.Now().Add(365 * 24 * time.Hour)

	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}

	state := oAuthState{
		Csrf:   base64.URLEncoding.EncodeToString(b),
		Return: returnPath,
		Link:   link,
	}

	// Secure flag is not set to allow Semaphore to be used without HTTPS inside private networks
	cookie := http.Cookie{
		Name:     "oauthstate",
		Value:    state.Csrf,
		Expires:  expiration,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, &cookie)

	stateBytes, err := json.Marshal(state)
	if err != nil {
		panic(err)
	}

	return base64.URLEncoding.EncodeToString(stateBytes)
}

type claimResult struct {
	sub           string
	username      string
	name          string
	email         string
	emailVerified bool
}

// emailVerifiedClaim reads the standard OIDC email_verified claim as a
// tri-state: value is whether it is true, present is whether the provider sent
// it at all. Some providers (e.g. AWS Cognito) send it as the string
// "true"/"false".
func emailVerifiedClaim(claims map[string]any) (value bool, present bool) {
	switch v := claims["email_verified"].(type) {
	case bool:
		return v, true
	case string:
		return v == "true", true
	default:
		return false, false
	}
}

// resolveEmailVerified decides whether the provider's email may be trusted to
// match an existing account. An explicit email_verified=false is NEVER trusted,
// regardless of require_verified_email — otherwise an IdP that lets users type
// any email could adopt someone else's account (Grafana CVE-2023-3128 class).
// When require_verified_email is off, an absent claim is treated as verified to
// accommodate providers (e.g. Okta) that omit it entirely; when on, the claim
// must be explicitly present and true.
func resolveEmailVerified(claims map[string]any, provider util.OidcProvider) bool {
	value, present := emailVerifiedClaim(claims)

	if provider.RequireVerifiedEmail {
		return present && value
	}

	if present {
		return value
	}

	return true
}

func parseClaim(str string, claims map[string]any) (string, bool) {
	for _, s := range strings.Split(str, "|") {
		s = strings.TrimSpace(s)

		if s == "" {
			continue
		}

		if strings.Contains(s, "{{") {
			tpl, err := template.New("").Parse(s)
			if err != nil {
				return "", false
			}

			buff := bytes.NewBufferString("")

			if err = tpl.Execute(buff, claims); err != nil {
				return "", false
			}

			res := buff.String()

			return res, res != ""
		}

		res, ok := claims[s].(string)
		if res != "" && ok {
			return res, ok
		}
	}

	return "", false
}

func prepareClaims(claims map[string]any) {
	for k, v := range claims {
		switch v := v.(type) {
		case float64:
			f := v
			i := int64(f)
			if float64(i) == f {
				claims[k] = i
			}
		case float32:
			f := v
			i := int64(f)
			if float32(i) == f {
				claims[k] = i
			}
		}
	}
}

func parseClaims(claims map[string]any, provider util.ClaimsProvider) (res claimResult, err error) {
	var ok bool
	res.email, ok = parseClaim(provider.GetEmailClaim(), claims)

	if !ok {
		err = fmt.Errorf("claim '%s' missing or has bad format", provider.GetEmailClaim())
		return
	}

	res.username, ok = parseClaim(provider.GetUsernameClaim(), claims)
	if !ok {
		res.username = getRandomUsername()
	}

	res.name, ok = parseClaim(provider.GetNameClaim(), claims)
	if !ok {
		res.name = getRandomProfileName()
	}

	return
}

// oidcEmailVerified reports whether the userinfo email may be used to match
// existing accounts. Always true unless the provider opted into
// require_verified_email; then the email_verified claim must be true.
func oidcEmailVerified(userInfo *oidc.UserInfo, provider util.OidcProvider) bool {
	rawClaims := make(map[string]any)
	if err := userInfo.Claims(&rawClaims); err != nil {
		return false
	}

	return resolveEmailVerified(rawClaims, provider)
}

func claimOidcUserInfo(userInfo *oidc.UserInfo, provider util.OidcProvider) (res claimResult, err error) {
	claims := make(map[string]any)
	if err = userInfo.Claims(&claims); err != nil {
		return
	}

	prepareClaims(claims)

	res, err = parseClaims(claims, &provider)
	res.sub = userInfo.Subject
	res.emailVerified = resolveEmailVerified(claims, provider)
	return
}

func claimOidcToken(idToken *oidc.IDToken, provider util.OidcProvider) (res claimResult, err error) {
	claims := make(map[string]any)
	if err = idToken.Claims(&claims); err != nil {
		return
	}

	prepareClaims(claims)

	res, err = parseClaims(claims, &provider)
	res.sub = idToken.Subject
	res.emailVerified = resolveEmailVerified(claims, provider)
	return
}

func getRandomUsername() string {
	return random.String(16)
}

func getRandomProfileName() string {
	return "Anonymous"
}

func getSecretFromFile(source string) (string, error) {
	content, err := os.ReadFile(source)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

// oidcSuccessRedirectURL builds the post-login redirect. url.JoinPath drops the
// leading slash when webHost is empty, which would make the redirect relative to
// the callback path instead of the web root.
func oidcSuccessRedirectURL(webHost string, redirectPath string) (string, error) {
	redirectPath = "/" + strings.TrimLeft(redirectPath, "/")

	if webHost == "" {
		return redirectPath, nil
	}

	return url.JoinPath(webHost, redirectPath)
}

func oidcRedirect(w http.ResponseWriter, r *http.Request) {
	oidcRedirectWithTOTPService(nil, w, r)
}
