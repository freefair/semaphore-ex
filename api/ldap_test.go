package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ldapServiceStub struct {
	providers           []pro_interfaces.LDAPProviderConfiguration
	loginProviders      []pro_interfaces.LDAPLoginProvider
	user                db.User
	err                 error
	configureRequest    pro_interfaces.LDAPConfigureRequest
	testRequest         pro_interfaces.LDAPTestRequest
	stateRequest        pro_interfaces.LDAPStateRequest
	authRequest         pro_interfaces.LDAPAuthenticationRequest
	linkRequest         pro_interfaces.LDAPLinkRequest
	linked              bool
	recoveryAllowed     bool
	groupMappings       []pro_interfaces.LDAPGroupMapping
	groupPreview        pro_interfaces.LDAPGroupPreview
	groupHistory        []db.LDAPGroupReconciliation
	groupSaveRequest    pro_interfaces.LDAPGroupMappingRequest
	groupDeleteRequest  pro_interfaces.LDAPGroupMappingDeleteRequest
	groupPreviewRequest pro_interfaces.LDAPGroupPreviewRequest
	groupApplyRequest   pro_interfaces.LDAPGroupApplyRequest
}

func (*ldapServiceStub) Initialize(context.Context) error { return nil }
func (s *ldapServiceStub) LoginProviders(context.Context) ([]pro_interfaces.LDAPLoginProvider, error) {
	return s.loginProviders, s.err
}
func (s *ldapServiceStub) AllowLocalRecovery(context.Context, string) (bool, error) {
	return s.recoveryAllowed, s.err
}
func (s *ldapServiceStub) Authenticate(
	_ context.Context, request pro_interfaces.LDAPAuthenticationRequest,
) (db.User, bool, error) {
	s.authRequest = request
	return s.user, s.linked, s.err
}
func (s *ldapServiceStub) Link(_ context.Context, request pro_interfaces.LDAPLinkRequest) (bool, error) {
	s.linkRequest = request
	return s.linked, s.err
}
func (s *ldapServiceStub) Providers(context.Context) ([]pro_interfaces.LDAPProviderConfiguration, error) {
	return s.providers, s.err
}
func (s *ldapServiceStub) Configure(
	_ context.Context, request pro_interfaces.LDAPConfigureRequest,
) (pro_interfaces.LDAPProviderConfiguration, error) {
	s.configureRequest = request
	if s.err != nil {
		return pro_interfaces.LDAPProviderConfiguration{}, s.err
	}
	return pro_interfaces.LDAPProviderConfiguration{
		ID: request.Provider.ID, BindPasswordConfigured: request.Provider.BindPassword != "",
	}, nil
}
func (s *ldapServiceStub) Test(
	_ context.Context, request pro_interfaces.LDAPTestRequest,
) (pro_interfaces.LDAPReadiness, error) {
	s.testRequest = request
	return pro_interfaces.LDAPReadiness{Status: pro_interfaces.LDAPReadinessReady}, s.err
}
func (s *ldapServiceStub) SetState(
	_ context.Context, request pro_interfaces.LDAPStateRequest,
) (pro_interfaces.LDAPProviderConfiguration, error) {
	s.stateRequest = request
	return pro_interfaces.LDAPProviderConfiguration{ID: request.ProviderID, State: request.State}, s.err
}
func (s *ldapServiceStub) Transitions(context.Context, string) ([]db.LDAPCapabilityTransition, error) {
	return []db.LDAPCapabilityTransition{}, s.err
}
func (s *ldapServiceStub) GroupMappings(context.Context, string) ([]pro_interfaces.LDAPGroupMapping, error) {
	return s.groupMappings, s.err
}
func (s *ldapServiceStub) SaveGroupMapping(_ context.Context, request pro_interfaces.LDAPGroupMappingRequest) (pro_interfaces.LDAPGroupMapping, error) {
	s.groupSaveRequest = request
	return request.Mapping, s.err
}
func (s *ldapServiceStub) DeleteGroupMapping(_ context.Context, request pro_interfaces.LDAPGroupMappingDeleteRequest) error {
	s.groupDeleteRequest = request
	return s.err
}
func (s *ldapServiceStub) PreviewGroupMappings(_ context.Context, request pro_interfaces.LDAPGroupPreviewRequest) (pro_interfaces.LDAPGroupPreview, error) {
	s.groupPreviewRequest = request
	return s.groupPreview, s.err
}
func (s *ldapServiceStub) ApplyGroupPreview(_ context.Context, request pro_interfaces.LDAPGroupApplyRequest) (pro_interfaces.LDAPGroupPreview, error) {
	s.groupApplyRequest = request
	return s.groupPreview, s.err
}
func (s *ldapServiceStub) ReconcileGroupMappings(_ context.Context, request pro_interfaces.LDAPGroupPreviewRequest) (pro_interfaces.LDAPGroupPreview, error) {
	s.groupPreviewRequest = request
	return s.groupPreview, s.err
}
func (s *ldapServiceStub) GroupReconciliationHistory(context.Context, string, int) ([]db.LDAPGroupReconciliation, error) {
	return s.groupHistory, s.err
}

func TestLDAPControllerConfigureKeepsBindCredentialWriteOnly(t *testing.T) {
	service := &ldapServiceStub{}
	audit := &auditRecorderStub{}
	controller := NewLDAPController(service, audit)
	request := httptest.NewRequest(http.MethodPut, "/api/capabilities/ldap", strings.NewReader(`{
		"id":"corp","display_name":"Corporate LDAP","server_url":"ldaps://ldap.example.test:636",
		"tls_mode":"ldaps","trust_mode":"system","bind_dn":"cn=bind,dc=example,dc=test",
		"bind_password":"bind-secret","search_base_dn":"ou=users,dc=example,dc=test",
		"user_filter":"(uid={{username}})","identity_attribute":"entryUUID",
		"username_attribute":"uid","name_attribute":"cn","email_attribute":"mail"
	}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.Configure(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "bind-secret", service.configureRequest.Provider.BindPassword)
	assert.NotContains(t, recorder.Body.String(), "bind-secret")
	assert.Contains(t, recorder.Body.String(), `"bind_password_configured":true`)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionLDAPConfigure, audit.events[0].Action)
	assert.NoError(t, audit.events[0].Validate())
}

func TestLDAPControllerReadinessAndStateContracts(t *testing.T) {
	service := &ldapServiceStub{}
	controller := NewLDAPController(service, nil)
	actor := &db.User{ID: 7, Admin: true}
	testRequest := httptest.NewRequest(http.MethodPost, "/api/capabilities/ldap/test", strings.NewReader(`{
		"provider_id":"corp","username":"probe","password":"directory-proof",
		"recovery_admin_user_id":7,"recovery_admin_password":"local-proof"
	}`))
	testRequest = helpers.SetContextValue(testRequest, "user", actor)
	testRecorder := httptest.NewRecorder()
	controller.Test(testRecorder, testRequest)
	assert.Equal(t, http.StatusOK, testRecorder.Code)
	assert.Equal(t, "corp", service.testRequest.ProviderID)
	assert.Equal(t, "local-proof", service.testRequest.RecoveryAdminPassword)

	stateRequest := httptest.NewRequest(http.MethodPut, "/api/capabilities/ldap/state", strings.NewReader(`{
		"provider_id":"corp","state":"selected_users","selected_user_ids":[11,12]
	}`))
	stateRequest = helpers.SetContextValue(stateRequest, "user", actor)
	stateRecorder := httptest.NewRecorder()
	controller.SetState(stateRecorder, stateRequest)
	assert.Equal(t, http.StatusOK, stateRecorder.Code)
	assert.Equal(t, pro_interfaces.LDAPStateSelectedUsers, service.stateRequest.State)
	assert.Equal(t, []int{11, 12}, service.stateRequest.SelectedUserIDs)
}

func TestWriteLDAPErrorReturnsStableSecurityStatuses(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{pro_interfaces.ErrLDAPInvalidCredentials, http.StatusUnauthorized, "LDAP_INVALID_CREDENTIALS"},
		{pro_interfaces.ErrLDAPProviderUnavailable, http.StatusServiceUnavailable, "LDAP_PROVIDER_UNAVAILABLE"},
		{pro_interfaces.ErrLDAPReferral, http.StatusServiceUnavailable, "LDAP_PROVIDER_UNAVAILABLE"},
		{pro_interfaces.ErrLDAPThrottled, http.StatusTooManyRequests, "LDAP_THROTTLED"},
		{pro_interfaces.ErrLDAPIdentityCollision, http.StatusConflict, "LDAP_IDENTITY_COLLISION"},
		{pro_interfaces.ErrLDAPReconfigurationRequiresInactive, http.StatusConflict, "LDAP_RECONFIGURATION_REQUIRES_INACTIVE"},
		{pro_interfaces.ErrLDAPReadiness, http.StatusConflict, "LDAP_ADMIN_RECOVERY_NOT_READY"},
		{pro_interfaces.ErrLDAPDisabled, http.StatusForbidden, "LDAP_DISABLED"},
		{pro_interfaces.ErrLDAPProviderNotFound, http.StatusNotFound, "LDAP_PROVIDER_NOT_FOUND"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		writeLDAPError(recorder, test.err)
		assert.Equal(t, test.status, recorder.Code)
		assert.Contains(t, recorder.Body.String(), test.code)
	}
}

func TestLDAPControllerAuditClassifiesDirectoryFailuresWithoutDiagnostics(t *testing.T) {
	service := &ldapServiceStub{err: pro_interfaces.ErrLDAPProviderUnavailable}
	audit := &auditRecorderStub{}
	controller := NewLDAPController(service, audit)
	request := httptest.NewRequest(http.MethodPost, "/api/capabilities/ldap/test", strings.NewReader(`{
		"provider_id":"corp","username":"probe","password":"secret",
		"recovery_admin_user_id":7,"recovery_admin_password":"local-secret"
	}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	recorder := httptest.NewRecorder()

	controller.Test(recorder, request)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditReasonProviderError, audit.events[0].Reason)
	assert.NotContains(t, recorder.Body.String(), "secret")
	assert.NoError(t, audit.events[0].Validate())
}

func TestManagedLDAPLoginMapsOutageAndAuditsOutcome(t *testing.T) {
	service := &ldapServiceStub{err: pro_interfaces.ErrLDAPProviderUnavailable}
	audit := &auditRecorderStub{}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"auth":"jdoe","password":"secret","method":"ldap","provider":"corp"}`))
	recorder := httptest.NewRecorder()

	loginWithIdentityServices(nil, service, audit, recorder, request)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "corp", service.authRequest.ProviderID)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionLDAPLogin, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditReasonProviderError, audit.events[0].Reason)
	assert.NoError(t, audit.events[0].Validate())
}

func TestManagedLDAPLoginInvalidCredentialsRecordsRootLoginFailure(t *testing.T) {
	service := &ldapServiceStub{err: pro_interfaces.ErrLDAPInvalidCredentials}
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"auth":"jdoe","password":"wrong","method":"ldap","provider":"corp"}`)), "store", setupSessionTest(t))
	request, recorded := withAuditRecorder(request)
	recorder := httptest.NewRecorder()

	loginWithIdentityServices(nil, service, nil, recorder, request)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	event := onlyEvent(t, recorded, audit.AuthLogin)
	assert.Equal(t, audit.OutcomeFailure, event.Event.Outcome)
	assert.Equal(t, audit.ReasonInvalidCredentials, event.Event.Reason)
	assert.Equal(t, audit.AuthMethodMetadata{Method: audit.LoginMethodLDAP, Provider: "corp"}, event.Event.Metadata)
}

func TestManagedLDAPLoginRecordsAuthoritativeProvisioningOutcome(t *testing.T) {
	for _, test := range []struct {
		name        string
		provisioned bool
		want        []audit.Kind
	}{
		{name: "new identity", provisioned: true, want: []audit.Kind{audit.IAMUserAutoProvision, audit.AuthLogin}},
		{name: "existing identity", provisioned: false, want: []audit.Kind{audit.AuthLogin}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := setupSessionTest(t)
			user := createUserOptionsTestUser(t, store, "ldap-"+test.name)
			request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/auth/login",
				strings.NewReader(`{"auth":"directory","password":"secret","method":"ldap","provider":"corp"}`)), "store", store)
			request, recorded := withAuditRecorder(request)

			loginWithIdentityServices(nil, &ldapServiceStub{user: user, linked: test.provisioned}, nil, httptest.NewRecorder(), request)

			kinds, err := recorded.Kinds()
			require.NoError(t, err)
			assert.Equal(t, test.want, kinds)
		})
	}
}

func TestManagedLDAPLinkRecordsOnlyCreatedIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		linked bool
		want   []audit.Kind
	}{
		{name: "created", linked: true, want: []audit.Kind{audit.IAMExternalIdentityLink}},
		{name: "existing", linked: false, want: []audit.Kind{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := setupSessionTest(t)
			user := createUserOptionsTestUser(t, store, "ldap-link-"+test.name)
			request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap",
				strings.NewReader(`{"username":"`+user.Username+`","password":"secret","provider":"corp"}`)), "store", store)
			request = helpers.SetContextValue(request, "user", &user)
			request, recorded := withAuditRecorder(request)

			linkLdapIdentityWithService(&ldapServiceStub{linked: test.linked}, nil, httptest.NewRecorder(), request)

			kinds, err := recorded.Kinds()
			require.NoError(t, err)
			assert.Equal(t, test.want, kinds)
		})
	}
}

func TestManagedLDAPMetadataExposesRecoveryOnlyPasswordPath(t *testing.T) {
	setupLoginConfig()
	utilConfig := util.Config
	utilConfig.PasswordLoginDisable = true
	service := &ldapServiceStub{loginProviders: []pro_interfaces.LDAPLoginProvider{{
		ID: "corp", Name: "Corporate LDAP", State: pro_interfaces.LDAPStateActive,
	}}}
	request := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	recorder := httptest.NewRecorder()

	loginWithIdentityServices(nil, service, nil, recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"local_recovery_only":true`)
	assert.Contains(t, recorder.Body.String(), `"login_with_password":true`)
	assert.NotContains(t, recorder.Body.String(), `"name":"LDAP"`)
}

func TestManagedLDAPRecoveryAdminBypassesGlobalPasswordDisable(t *testing.T) {
	setupLoginConfig()
	util.Config.PasswordLoginDisable = true
	service := &ldapServiceStub{recoveryAllowed: false}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login",
		strings.NewReader(`{"auth":"other","password":"secret","method":"password"}`))
	recorder := httptest.NewRecorder()

	loginWithIdentityServices(nil, service, nil, recorder, request)

	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
	service.recoveryAllowed = true
}

func TestManagedLDAPLinkUsesIdentityServiceAndAuditsCollision(t *testing.T) {
	service := &ldapServiceStub{err: pro_interfaces.ErrLDAPIdentityCollision}
	audit := &auditRecorderStub{}
	request := httptest.NewRequest(http.MethodPost, "/api/user/identities/ldap",
		strings.NewReader(`{"provider":"corp","username":"jdoe","password":"secret"}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 11})
	recorder := httptest.NewRecorder()

	linkLdapIdentityWithService(service, audit, recorder, request)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Equal(t, 11, service.linkRequest.ActorID)
	assert.Equal(t, "corp", service.linkRequest.ProviderID)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionLDAPLink, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditReasonLDAPPolicy, audit.events[0].Reason)
	assert.NoError(t, audit.events[0].Validate())
}

var _ pro_interfaces.LDAPService = (*ldapServiceStub)(nil)
