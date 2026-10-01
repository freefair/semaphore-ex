package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTOTPControllerOidcChallengeRecordsLoginAfterPasscode(t *testing.T) {
	store := setupSessionTest(t)
	user := createUserOptionsTestUser(t, store, "oidc-challenge")
	user.Totp = &db.UserTotp{}
	meta := audit.AuthMethodMetadata{Method: audit.LoginMethodOIDC, Provider: "corp"}
	loginResponse := httptest.NewRecorder()

	verified := createSessionWithMetadata(loginResponse,
		helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/", nil), "store", store), user, meta,
		&totpServiceStub{requirement: pro_interfaces.TOTPSessionChallenge})
	assert.True(t, verified)

	verifyRequest := requestWithSessionCookie(store, loginResponse, http.MethodPost, "/api/auth/verify", `{"passcode":"123456"}`)
	session, ok := getSession(verifyRequest)
	require.True(t, ok)
	assert.False(t, session.Verified)
	verify, recorded := withAuditRecorder(verifyRequest)
	NewTOTPController(&totpServiceStub{sessionStore: store}, nil).VerifySession(httptest.NewRecorder(), verify)

	kinds, err := recorded.Kinds()
	require.NoError(t, err)
	require.Equal(t, []audit.Kind{audit.AuthMFAVerifyTOTP, audit.AuthLogin}, kinds)
	assert.Equal(t, meta, recorded.All()[1].Event.Metadata)
	session, ok = getSession(verify)
	require.True(t, ok)
	assert.True(t, session.Verified)
}

func TestTOTPControllerOidcRecoveryRecordsLoginAfterRecovery(t *testing.T) {
	store := setupSessionTest(t)
	user := createUserOptionsTestUser(t, store, "oidc-recovery")
	user.Totp = &db.UserTotp{}
	meta := audit.AuthMethodMetadata{Method: audit.LoginMethodOIDC, Provider: "corp"}
	loginResponse := httptest.NewRecorder()

	verified := createSessionWithMetadata(loginResponse,
		helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/", nil), "store", store), user, meta,
		&totpServiceStub{requirement: pro_interfaces.TOTPSessionChallenge})
	assert.True(t, verified)

	recoveryRequest := requestWithSessionCookie(store, loginResponse, http.MethodPost, "/api/auth/recovery", `{"recovery_code":"RECOVERY"}`)
	session, ok := getSession(recoveryRequest)
	require.True(t, ok)
	assert.False(t, session.Verified)
	recovery, recorded := withAuditRecorder(recoveryRequest)
	NewTOTPController(&totpServiceStub{sessionStore: store}, nil).RecoverSession(httptest.NewRecorder(), recovery)

	kinds, err := recorded.Kinds()
	require.NoError(t, err)
	require.Equal(t, []audit.Kind{audit.AuthMFARecover, audit.AuthLogin}, kinds)
	assert.Equal(t, meta, recorded.All()[1].Event.Metadata)
	session, ok = getSession(recovery)
	require.True(t, ok)
	assert.True(t, session.Verified)
}

func TestTOTPControllerEnrollmentAcknowledgementRecordsLogin(t *testing.T) {
	store := setupSessionTest(t)
	user := createUserOptionsTestUser(t, store, "oidc-enrollment")
	session, err := store.CreateSession(db.Session{UserID: user.ID, Created: tz.Now(), LastActive: tz.Now(), VerificationMethod: db.SessionVerificationTotpEnrollment})
	require.NoError(t, err)
	request := sessionRequest(t, store, session, audit.AuthMethodMetadata{Method: audit.LoginMethodOIDC, Provider: "corp"}, `{"stored":true}`)
	request = mux.SetURLVars(request, map[string]string{"totp_id": "17"})
	request, recorded := withAuditRecorder(request)

	NewTOTPController(&totpServiceStub{sessionStore: store}, nil).AcknowledgeSessionRecoveryCodes(httptest.NewRecorder(), request)

	kinds, err := recorded.Kinds()
	require.NoError(t, err)
	require.Equal(t, []audit.Kind{audit.IAMMFAEnable, audit.AuthLogin}, kinds)
	assert.Equal(t, audit.UserActor(user.ID, user.Username, audit.AuthSession, ""), recorded.All()[0].Actor)
	assert.Equal(t, audit.UserTarget(user.ID, user.Username), recorded.All()[0].Event.Target)
	assert.Equal(t, audit.AuthMethodMetadata{Method: audit.LoginMethodOIDC, Provider: "corp"}, recorded.All()[1].Event.Metadata)
	persisted, err := store.GetSession(user.ID, session.ID)
	require.NoError(t, err)
	assert.True(t, persisted.Verified)
}

func TestTOTPControllerEnrollmentQRRecordsSessionActorAndTarget(t *testing.T) {
	store := setupSessionTest(t)
	user := createUserOptionsTestUser(t, store, "oidc-enrollment-qr")
	session, err := store.CreateSession(db.Session{UserID: user.ID, Created: tz.Now(), LastActive: tz.Now(), VerificationMethod: db.SessionVerificationTotpEnrollment})
	require.NoError(t, err)
	request := sessionRequest(t, store, session, audit.AuthMethodMetadata{Method: audit.LoginMethodOIDC, Provider: "corp"}, "")
	request.Method = http.MethodGet
	request = mux.SetURLVars(request, map[string]string{"totp_id": "17"})
	request, recorded := withAuditRecorder(request)

	NewTOTPController(&totpServiceStub{provisioningURI: "otpauth://totp/Semaphore:enrollment?secret=JBSWY3DPEHPK3PXP&issuer=Semaphore"}, nil).SessionQR(httptest.NewRecorder(), request)

	event := onlyEvent(t, recorded, audit.IAMMFAViewQR)
	assert.Equal(t, audit.UserActor(user.ID, user.Username, audit.AuthSession, ""), event.Actor)
	assert.Equal(t, audit.UserTarget(user.ID, user.Username), event.Event.Target)
}

func requestWithSessionCookie(store db.Store, loginResponse *httptest.ResponseRecorder, method string, path string, body string) *http.Request {
	request := helpers.SetContextValue(httptest.NewRequest(method, path, strings.NewReader(body)), "store", store)
	for _, cookie := range loginResponse.Result().Cookies() {
		request.AddCookie(cookie)
	}
	return request
}

func sessionRequest(t *testing.T, store db.Store, session db.Session, meta audit.AuthMethodMetadata, body string) *http.Request {
	t.Helper()
	encoded, err := util.Cookie.Encode("semaphore", map[string]any{
		"user": session.UserID, "session": session.ID, "method": meta.Method, "provider": meta.Provider,
	})
	require.NoError(t, err)
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPost,
		"/api/auth/totp/enroll/17/recovery-codes/acknowledge", strings.NewReader(body)), "store", store)
	request.AddCookie(&http.Cookie{Name: "semaphore", Value: encoded})
	return request
}
