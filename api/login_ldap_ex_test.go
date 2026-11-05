package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	identityServices "github.com/semaphoreui/semaphore/services/identity"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLegacyDefaultMethodLDAPFailureRemainsUnauthorized(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	setupLoginConfig()
	util.Config.LdapServer = "127.0.0.1:1"

	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"auth":"jdoe","password":"wrong"}`))
	request = helpers.SetContextValue(request, "store", store)
	recorder := httptest.NewRecorder()

	login(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestManagedLDAPIgnoresLegacyGroupSettings(t *testing.T) {
	store := setupSessionTest(t)
	user := createUserOptionsTestUser(t, store, "managed-ldap")
	util.Config.LdapGroupSearchDN = "ou=legacy-groups"
	service := &ldapServiceStub{user: user}
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"auth":"directory","password":"secret","method":"ldap","provider":"corp"}`)), "store", store)
	request, _ = withAuditRecorder(request)
	recorder := httptest.NewRecorder()
	loginWithIdentityServices(nil, service, nil, recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "corp", service.authRequest.ProviderID)
}

func TestManagedLDAPLinkIgnoresLegacyGroupSettings(t *testing.T) {
	setupLoginConfig()
	util.Config.LdapGroupSearchDN = "ou=legacy-groups"
	service := &ldapServiceStub{}
	request := httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap",
		strings.NewReader(`{"username":"jdoe","password":"password","provider":"corp"}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
	recorder := httptest.NewRecorder()
	linkLdapIdentityWithService(service, nil, recorder, request)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	require.Equal(t, "corp", service.linkRequest.ProviderID)
}

func TestLegacyLDAPRejectsEachGroupSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*util.LdapProvider)
	}{
		{"search DN", func(p *util.LdapProvider) { p.GroupSearchDN = "ou=groups" }},
		{"search filter", func(p *util.LdapProvider) { p.GroupSearchFilter = "(cn=*)" }},
		{"name attribute", func(p *util.LdapProvider) { p.GroupNameAttribute = "cn" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupLoginConfig()
			provider := util.Config.LdapProviders["corp"]
			tc.set(&provider)
			util.Config.LdapProviders["corp"] = provider
			_, _, err := authenticateLegacyLDAPProfile(context.Background(), "corp", "user", "password")
			require.ErrorIs(t, err, errLegacyLDAPGroupMappingUnsupported)
		})
	}
}

func TestFlatLegacyLDAPRejectsEachGroupSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*util.ConfigType)
	}{
		{"search DN", func(c *util.ConfigType) { c.LdapGroupSearchDN = "ou=groups" }},
		{"search filter", func(c *util.ConfigType) { c.LdapGroupSearchFilter = "(cn=*)" }},
		{"name attribute", func(c *util.ConfigType) { c.LdapGroupNameAttribute = "cn" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupLoginConfig()
			tc.set(util.Config)
			_, _, err := authenticateLegacyLDAPProfile(context.Background(), "ldap", "user", "password")
			require.ErrorIs(t, err, errLegacyLDAPGroupMappingUnsupported)
		})
	}
}

func TestLegacyLDAPLinkRejectsGroupSettings(t *testing.T) {
	setupLoginConfig()
	util.Config.LdapGroupSearchFilter = "(cn=*)"
	request := httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap",
		strings.NewReader(`{"username":"jdoe","password":"password"}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
	recorder := httptest.NewRecorder()
	linkLdapIdentityWithService(nil, nil, recorder, request)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "LDAP_GROUP_MAPPING_UNSUPPORTED")
}

func TestLegacyLDAPGroupConfigurationFailsClosed(t *testing.T) {
	for _, method := range []string{"", `,"method":"ldap","provider":"corp"`} {
		t.Run(method, func(t *testing.T) {
			store := sqldb.InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			setupLoginConfig()
			util.Config.LdapGroupSearchDN = "ou=groups,dc=example,dc=test"
			provider := util.Config.LdapProviders["corp"]
			provider.GroupSearchDN = "ou=groups,dc=example,dc=test"
			util.Config.LdapProviders["corp"] = provider
			request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
				strings.NewReader(`{"auth":"jdoe","password":"password"`+method+`}`))
			request = helpers.SetContextValue(request, "store", store)
			recorder := httptest.NewRecorder()

			login(recorder, request)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), "LDAP_GROUP_MAPPING_UNSUPPORTED")
			require.Empty(t, recorder.Result().Cookies())
		})
	}
}

func TestProductionLDAPServiceRejectsLegacyGroupsWithoutShadowingManaged(t *testing.T) {
	store := setupSessionTest(t)
	setupLoginConfig()
	util.Config.LdapProviders = map[string]util.LdapProvider{"qa-directory": {DisplayName: "Legacy QA", GroupSearchDN: "ou=groups"}}
	service := proFeatures.NewLDAPService(store, proFeatures.NewCapabilityProvider(store), identityServices.NewLDAPClient())
	require.NoError(t, service.Initialize(context.Background()))
	metadata := func() loginMetadata {
		w := httptest.NewRecorder()
		loginWithIdentityServices(nil, service, nil, w, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var result loginMetadata
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result
	}
	require.Len(t, metadata().LdapProviders, 1)
	require.Equal(t, "qa-directory", metadata().LdapProviders[0].ID)
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"auth":"test","password":"test","method":"ldap","provider":"qa-directory"}`)), "store", store)
	w := httptest.NewRecorder()
	loginWithIdentityServices(nil, service, nil, w, request)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "LDAP_GROUP_MAPPING_UNSUPPORTED")
	require.Empty(t, w.Result().Cookies())
	createUserOptionsTestUser(t, store, "recovery")
	passwordRequest := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"auth":"recovery","password":"verystrongpassword1","method":"password"}`)), "store", store)
	passwordResponse := httptest.NewRecorder()
	loginWithIdentityServices(nil, service, nil, passwordResponse, passwordRequest)
	require.Equal(t, http.StatusNoContent, passwordResponse.Code)
	link := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap", strings.NewReader(`{"username":"test","password":"test","provider":"qa-directory"}`)), "user", &db.User{ID: 1})
	w = httptest.NewRecorder()
	audit := &auditRecorderStub{}
	linkLdapIdentityWithService(service, audit, w, link)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "LDAP_GROUP_MAPPING_UNSUPPORTED")
	require.Len(t, audit.events, 1)
	require.Equal(t, pro_interfaces.AuditActionLDAPLink, audit.events[0].Action)
	require.Equal(t, pro_interfaces.AuditOutcomeFailure, audit.events[0].Outcome)
	require.Equal(t, pro_interfaces.AuditReasonProviderError, audit.events[0].Reason)
	require.NoError(t, audit.events[0].Validate())
	now := time.Now().UTC()
	require.NoError(t, store.SaveLDAPProvider(db.LDAPProvider{ID: "qa-directory", DisplayName: "Managed QA", State: "disabled", ServerURL: "ldaps://ldap.example.test:636", TLSMode: "ldaps", TrustMode: "system", BindDN: "cn=bind,dc=example,dc=test", EncryptedBindPassword: "encrypted", SearchBaseDN: "dc=example,dc=test", UserFilter: "(uid={{username}})", IdentityAttribute: "entryUUID", UsernameAttribute: "uid", NameAttribute: "cn", EmailAttribute: "mail", ReadinessStatus: "untested", ReadinessCode: "configuration_changed", Created: now, Updated: now}))
	require.Empty(t, metadata().LdapProviders)
	request = helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"auth":"test","password":"test","method":"ldap","provider":"qa-directory"}`)), "store", store)
	w = httptest.NewRecorder()
	loginWithIdentityServices(nil, service, nil, w, request)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "LDAP_DISABLED")
	util.Config.LdapProviders["QA-DIRECTORY"] = util.Config.LdapProviders["qa-directory"]
	require.Empty(t, metadata().LdapProviders)
	mixed := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"auth":"test","password":"test","method":"ldap","provider":"QA-DIRECTORY"}`)), "store", store)
	w = httptest.NewRecorder()
	loginWithIdentityServices(nil, service, nil, w, mixed)
	require.NotEqual(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, w.Body.String(), "LDAP_GROUP_MAPPING_UNSUPPORTED")
	link = helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap", strings.NewReader(`{"username":"test","password":"test","provider":"qa-directory"}`)), "user", &db.User{ID: 1})
	w = httptest.NewRecorder()
	linkLdapIdentityWithService(service, nil, w, link)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "LDAP_DISABLED")
}

type ldapProviderListFailureStub struct{ *ldapServiceStub }

func (*ldapProviderListFailureStub) Link(context.Context, pro_interfaces.LDAPLinkRequest) (bool, error) {
	return false, pro_interfaces.ErrLDAPProviderNotFound
}
func (*ldapProviderListFailureStub) Providers(context.Context) ([]pro_interfaces.LDAPProviderConfiguration, error) {
	return nil, errors.New("lookup failure")
}
func TestLDAPLinkAuditsManagedProviderLookupFailure(t *testing.T) {
	setupLoginConfig()
	provider := util.Config.LdapProviders["corp"]
	provider.GroupSearchDN = "ou=groups"
	util.Config.LdapProviders["corp"] = provider
	service := &ldapProviderListFailureStub{&ldapServiceStub{}}
	audit := &auditRecorderStub{}
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap", strings.NewReader(`{"username":"fixture","password":"fixture","provider":"corp"}`)), "user", &db.User{ID: 1})
	w := httptest.NewRecorder()
	linkLdapIdentityWithService(service, audit, w, request)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Len(t, audit.events, 1)
	require.Equal(t, pro_interfaces.AuditActionLDAPLink, audit.events[0].Action)
	require.Equal(t, pro_interfaces.AuditOutcomeFailure, audit.events[0].Outcome)
	require.Equal(t, pro_interfaces.AuditReasonOperationError, audit.events[0].Reason)
	require.NoError(t, audit.events[0].Validate())
}
