package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOIDCRedirectReconcilesOnlyVerifiedBoundedGroupClaims(t *testing.T) {
	tests := []struct {
		name          string
		groups        any
		missingPolicy string
		wantStatus    int
		wantValues    []string
		wantTrusted   bool
		wantPresent   bool
	}{
		{name: "array", groups: []string{"Engineering", "Auditors"}, wantStatus: http.StatusTemporaryRedirect, wantValues: []string{"auditors", "engineering"}, wantTrusted: true, wantPresent: true},
		{name: "scalar", groups: "Auditors", wantStatus: http.StatusTemporaryRedirect, wantValues: []string{"auditors"}, wantTrusted: true, wantPresent: true},
		{name: "missing preserves", missingPolicy: "preserve", wantStatus: http.StatusTemporaryRedirect, wantValues: []string{}, wantTrusted: false},
		{name: "missing clears", missingPolicy: "clear", wantStatus: http.StatusTemporaryRedirect, wantValues: []string{}, wantTrusted: true},
		{name: "malformed shape", groups: map[string]any{"role": "admin"}, wantStatus: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newOIDCGroupCallbackFixture(t, tt.groups, tt.missingPolicy)
			recorder := httptest.NewRecorder()
			request := fixture.callbackRequest(t, false)

			oidcRedirectWithIdentityServices(nil, fixture.service, recorder, request)

			assert.Equal(t, tt.wantStatus, recorder.Code)
			if tt.wantStatus != http.StatusTemporaryRedirect {
				assert.Empty(t, fixture.service.reconcileRequests)
				return
			}
			require.Len(t, fixture.service.reconcileRequests, 1)
			groupRequest := fixture.service.reconcileRequests[0]
			assert.Equal(t, "corp", groupRequest.ProviderID)
			assert.Equal(t, "login", groupRequest.Source)
			assert.Equal(t, tt.wantValues, groupRequest.Claim.Values)
			assert.Equal(t, tt.wantTrusted, groupRequest.Claim.Trusted)
			assert.Equal(t, tt.wantPresent, groupRequest.Claim.Present)
			assert.NotContains(t, recorder.Body.String(), fixture.rawIDToken)
		})
	}
}

func TestClaimOIDCTokenSkipsGroupParsingForLinkMode(t *testing.T) {
	fixture := newOIDCGroupCallbackFixture(t, map[string]any{"unexpected": true}, "preserve")
	claims, err := claimOidcToken(fixture.idToken, fixture.provider, false)

	require.NoError(t, err)
	assert.Nil(t, claims.groups)
}

func TestOIDCRedirectLinkModeSkipsMalformedGroupClaimAndReconciliation(t *testing.T) {
	fixture := newOIDCGroupCallbackFixture(t, map[string]any{"unexpected": true}, "preserve")
	user, err := fixture.store.CreateUserWithoutPassword(db.User{
		Username: "oidc-link-fixture", Name: "OIDC Link Fixture", Email: "oidc-link-fixture@example.test",
	})
	require.NoError(t, err)
	session, err := fixture.store.CreateSession(db.Session{
		UserID: user.ID, Created: time.Now(), LastActive: time.Now(), IP: "127.0.0.1", UserAgent: "test",
	})
	require.NoError(t, err)
	cookieValue, err := util.Cookie.Encode("semaphore", map[string]any{"user": session.UserID, "session": session.ID})
	require.NoError(t, err)
	request := fixture.callbackRequest(t, true)
	request.AddCookie(&http.Cookie{Name: "semaphore", Value: cookieValue})
	recorder := httptest.NewRecorder()

	oidcRedirectWithIdentityServices(nil, fixture.service, recorder, request)

	assert.Equal(t, http.StatusTemporaryRedirect, recorder.Code)
	assert.Empty(t, fixture.service.reconcileRequests)
}

type oidcGroupCallbackFixture struct {
	provider       util.OidcProvider
	idToken        *oidc.IDToken
	rawIDToken     string
	service        *oidcGroupMappingServiceStub
	store          *sql.SqlDb
	server         *httptest.Server
	original       *util.ConfigType
	originalCookie *securecookie.SecureCookie
}

func newOIDCGroupCallbackFixture(t *testing.T, groups any, missingPolicy string) *oidcGroupCallbackFixture {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	var token string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": server.URL, "authorization_endpoint": server.URL + "/auth",
				"token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/jwks",
			})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fixture-access-token", "token_type": "Bearer", "id_token": token})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
				Key: &privateKey.PublicKey, KeyID: "fixture-key", Algorithm: string(jose.RS256), Use: "sig",
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	claims := map[string]any{
		"iss": server.URL, "aud": "fixture-client", "sub": "fixture-subject",
		"email": "fixture@example.test", "preferred_username": "fixture", "name": "Fixture User",
		"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Add(-time.Minute).Unix(),
	}
	if groups != nil {
		claims["realm"] = map[string]any{"groups": groups}
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: privateKey}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "fixture-key"))
	require.NoError(t, err)
	token, err = jwt.Signed(signer).Claims(claims).Serialize()
	require.NoError(t, err)

	provider := util.OidcProvider{
		ClientID: "fixture-client", ClientSecret: "fixture-secret", EmailClaim: "email", UsernameClaim: "preferred_username", NameClaim: "name",
		AutoDiscovery:  server.URL,
		GroupClaimPath: "realm.groups", GroupClaimCaseInsensitive: true, GroupClaimMissingPolicy: missingPolicy,
	}
	original := util.Config
	originalCookie := util.Cookie
	store := sql.InitConfigCreateTestStore()
	util.Config = &util.ConfigType{OidcProviders: map[string]util.OidcProvider{"corp": provider}}
	util.Cookie = securecookie.New([]byte(strings.Repeat("h", 64)), []byte(strings.Repeat("e", 32)))
	oidcProvider, _, err := getOidcProvider("corp", context.Background(), "")
	require.NoError(t, err)
	idToken, err := oidcProvider.Verifier(&oidc.Config{ClientID: provider.ClientID}).Verify(context.Background(), token)
	require.NoError(t, err)
	fixture := &oidcGroupCallbackFixture{provider: provider, idToken: idToken, rawIDToken: token, service: &oidcGroupMappingServiceStub{}, store: store, server: server, original: original, originalCookie: originalCookie}
	t.Cleanup(fixture.close)
	return fixture
}

func (f *oidcGroupCallbackFixture) callbackRequest(t *testing.T, link bool) *http.Request {
	t.Helper()
	stateBytes, err := json.Marshal(oAuthState{Csrf: "fixture-csrf", Link: link})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/corp/redirect?code=fixture-code&state="+url.QueryEscape(base64.URLEncoding.EncodeToString(stateBytes)), nil)
	request.AddCookie(&http.Cookie{Name: "oauthstate", Value: "fixture-csrf"})
	request = helpers.SetContextValue(request, "store", f.store)
	return mux.SetURLVars(request, map[string]string{"provider": "corp"})
}

func (f *oidcGroupCallbackFixture) close() {
	if f.store != nil {
		f.store.Close()
		f.store = nil
	}
	if f.server != nil {
		f.server.Close()
		f.server = nil
	}
	util.Config = f.original
	util.Cookie = f.originalCookie
}
