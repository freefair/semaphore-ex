package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type totpServiceStub struct {
	beginRequest    pro_interfaces.TOTPEnrollmentRequest
	confirmRequest  pro_interfaces.TOTPConfirmationRequest
	resetRequest    pro_interfaces.TOTPResetRequest
	configureError  error
	verifyError     error
	recoverError    error
	verifiedCode    string
	recoveryCode    string
	requirementErr  error
	requirement     pro_interfaces.TOTPSessionRequirement
	sessionStore    db.Store
	provisioningURI string
	resetError      error
}

func (*totpServiceStub) Initialize(context.Context) error { return nil }
func (*totpServiceStub) Status(context.Context, int) (pro_interfaces.TOTPStatus, error) {
	return pro_interfaces.TOTPStatus{CapabilityState: pro_interfaces.CapabilityStateOptional}, nil
}
func (s *totpServiceStub) SessionRequirement(context.Context, int) (pro_interfaces.TOTPSessionRequirement, error) {
	return s.requirement, s.requirementErr
}
func (s *totpServiceStub) BeginEnrollment(
	_ context.Context,
	request pro_interfaces.TOTPEnrollmentRequest,
) (pro_interfaces.TOTPEnrollmentCeremony, error) {
	s.beginRequest = request
	return pro_interfaces.TOTPEnrollmentCeremony{ID: 17, ProvisioningURI: "otpauth://ceremony"}, nil
}
func (s *totpServiceStub) ProvisioningURI(context.Context, int, int, int) (string, error) {
	if s.provisioningURI == "" {
		return "", pro_interfaces.ErrTOTPNotFound
	}
	return s.provisioningURI, nil
}
func (s *totpServiceStub) ConfirmEnrollment(
	_ context.Context,
	request pro_interfaces.TOTPConfirmationRequest,
) (pro_interfaces.TOTPStatus, error) {
	s.confirmRequest = request
	return pro_interfaces.TOTPStatus{EnrollmentState: pro_interfaces.TOTPEnrollmentPendingRecoveryAck}, nil
}
func (s *totpServiceStub) AcknowledgeRecoveryCodes(_ context.Context, request pro_interfaces.TOTPRecoveryAcknowledgement) (pro_interfaces.TOTPStatus, error) {
	if s.sessionStore != nil {
		if err := s.sessionStore.VerifySession(request.TargetUserID, request.SessionID); err != nil {
			return pro_interfaces.TOTPStatus{}, err
		}
	}
	return pro_interfaces.TOTPStatus{EnrollmentState: pro_interfaces.TOTPEnrollmentActive}, nil
}
func (s *totpServiceStub) VerifyChallenge(_ context.Context, userID int, sessionID int, code string, _ time.Time) error {
	s.verifiedCode = code
	if s.verifyError != nil || s.sessionStore == nil {
		return s.verifyError
	}
	return s.sessionStore.VerifySession(userID, sessionID)
}
func (s *totpServiceStub) RecoverSession(_ context.Context, userID int, sessionID int, code string, _ time.Time) error {
	s.recoveryCode = code
	if s.recoverError != nil || s.sessionStore == nil {
		return s.recoverError
	}
	return s.sessionStore.VerifySession(userID, sessionID)
}
func (s *totpServiceStub) ResetEnrollment(_ context.Context, request pro_interfaces.TOTPResetRequest) error {
	s.resetRequest = request
	return s.resetError
}
func (s *totpServiceStub) Configure(
	_ context.Context,
	request pro_interfaces.TOTPConfigurationRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	if s.configureError != nil {
		return pro_interfaces.CapabilitySnapshot{}, s.configureError
	}
	decision := pro_interfaces.NewCapabilityDecision(
		pro_interfaces.CapabilityTOTP, request.State,
		pro_interfaces.CapabilityReasonCode(request.State),
		[]pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead}, nil,
	)
	return pro_interfaces.NewCapabilitySnapshot(pro_interfaces.CapabilityRequest{
		UserID: request.ActorID, IsAdmin: request.ActorIsAdmin, At: request.Now,
	}, []pro_interfaces.CapabilityDecision{decision}), nil
}
func (*totpServiceStub) Configuration(context.Context) (pro_interfaces.TOTPRolloutConfiguration, error) {
	return pro_interfaces.TOTPRolloutConfiguration{State: pro_interfaces.CapabilityStateOptional}, nil
}
func (*totpServiceStub) Transitions(context.Context) ([]db.TOTPCapabilityTransition, error) {
	return []db.TOTPCapabilityTransition{}, nil
}

func TestTOTPControllerEnrollmentAndConfirmationContracts(t *testing.T) {
	service := &totpServiceStub{}
	controller := NewTOTPController(service, nil)
	actor := &db.User{ID: 4}
	target := db.User{ID: 4}

	begin := httptest.NewRequest(http.MethodPost, "/api/users/4/2fas/totp",
		strings.NewReader(`{"reauthentication":"proof"}`))
	begin = helpers.SetContextValue(begin, "user", actor)
	begin = helpers.SetContextValue(begin, "_user", target)
	beginRecorder := httptest.NewRecorder()
	controller.BeginEnrollment(beginRecorder, begin)
	assert.Equal(t, http.StatusCreated, beginRecorder.Code)
	assert.Equal(t, 4, service.beginRequest.ActorID)
	assert.Equal(t, "proof", service.beginRequest.Reauthentication)

	confirm := httptest.NewRequest(http.MethodPost, "/api/users/4/2fas/totp/17/confirm",
		strings.NewReader(`{"reauthentication":"proof","passcode":"123456"}`))
	confirm = mux.SetURLVars(confirm, map[string]string{"totp_id": "17"})
	confirm = helpers.SetContextValue(confirm, "user", actor)
	confirm = helpers.SetContextValue(confirm, "_user", target)
	confirmRecorder := httptest.NewRecorder()
	controller.ConfirmEnrollment(confirmRecorder, confirm)
	assert.Equal(t, http.StatusOK, confirmRecorder.Code)
	assert.Equal(t, 17, service.confirmRequest.EnrollmentID)
	assert.Equal(t, "123456", service.confirmRequest.Passcode)
}

func TestTOTPControllerResetAndReadinessErrors(t *testing.T) {
	service := &totpServiceStub{configureError: pro_interfaces.ErrTOTPReadiness}
	controller := NewTOTPController(service, nil)
	actor := &db.User{ID: 3, Admin: true}
	target := db.User{ID: 9}

	reset := httptest.NewRequest(http.MethodDelete, "/api/users/9/2fas/totp/22", nil)
	reset = mux.SetURLVars(reset, map[string]string{"totp_id": "22"})
	reset = helpers.SetContextValue(reset, "user", actor)
	reset = helpers.SetContextValue(reset, "_user", target)
	resetRecorder := httptest.NewRecorder()
	controller.Reset(resetRecorder, reset)
	assert.Equal(t, http.StatusNoContent, resetRecorder.Code)
	assert.True(t, service.resetRequest.ActorIsAdmin)
	assert.Equal(t, 9, service.resetRequest.TargetUserID)

	configure := httptest.NewRequest(http.MethodPut, "/api/capabilities/totp",
		strings.NewReader(`{"state":"required"}`))
	configure = helpers.SetContextValue(configure, "user", actor)
	configureRecorder := httptest.NewRecorder()
	controller.Configure(configureRecorder, configure)
	assert.Equal(t, http.StatusConflict, configureRecorder.Code)
	assert.Contains(t, configureRecorder.Body.String(), "TOTP_ADMIN_RECOVERY_NOT_READY")
}

func TestTOTPControllerRecordsQRAndSuccessfulResetOnly(t *testing.T) {
	store := setupSessionTest(t)
	actor := &db.User{ID: 3, Username: "admin", Admin: true}
	target := db.User{ID: 9, Username: "target"}
	service := &totpServiceStub{provisioningURI: "otpauth://totp/Semaphore:target?secret=JBSWY3DPEHPK3PXP&issuer=Semaphore"}
	controller := NewTOTPController(service, nil)

	qr := httptest.NewRequest(http.MethodGet, "/api/users/9/2fas/totp/22/qr", nil)
	qr = mux.SetURLVars(qr, map[string]string{"totp_id": "22"})
	qr = helpers.SetContextValue(qr, "user", actor)
	qr = helpers.SetContextValue(qr, "_user", target)
	qr = helpers.SetContextValue(qr, "store", store)
	qr, recorded := withAuditRecorder(qr)
	qrResponse := httptest.NewRecorder()
	controller.QR(qrResponse, qr)
	assert.Equal(t, http.StatusOK, qrResponse.Code)
	onlyEvent(t, recorded, audit.IAMMFAViewQR)

	reset := httptest.NewRequest(http.MethodDelete, "/api/users/9/2fas/totp/22", nil)
	reset = mux.SetURLVars(reset, map[string]string{"totp_id": "22"})
	reset = helpers.SetContextValue(reset, "user", actor)
	reset = helpers.SetContextValue(reset, "_user", target)
	reset = helpers.SetContextValue(reset, "store", store)
	reset, recorded = withAuditRecorder(reset)
	controller.Reset(httptest.NewRecorder(), reset)
	onlyEvent(t, recorded, audit.IAMMFADisable)

	service.resetError = pro_interfaces.ErrTOTPNotFound
	missing := httptest.NewRequest(http.MethodDelete, "/api/users/9/2fas/totp/999", nil)
	missing = mux.SetURLVars(missing, map[string]string{"totp_id": "999"})
	missing = helpers.SetContextValue(missing, "user", actor)
	missing = helpers.SetContextValue(missing, "_user", target)
	missing = helpers.SetContextValue(missing, "store", store)
	missing, recorded = withAuditRecorder(missing)
	controller.Reset(httptest.NewRecorder(), missing)
	assert.Empty(t, recorded.All())
}

func TestTOTPControllerAuditSeparatesAdminActorFromTarget(t *testing.T) {
	store := setupSessionTest(t)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "totp-admin", Name: "TOTP Admin", Email: "totp-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	target := createUserOptionsTestUser(t, store, "totp-target")
	session, err := store.CreateSession(db.Session{UserID: admin.ID, Created: tz.Now(), LastActive: tz.Now()})
	require.NoError(t, err)
	service := &totpServiceStub{provisioningURI: "otpauth://totp/Semaphore:target?secret=JBSWY3DPEHPK3PXP&issuer=Semaphore"}
	controller := NewTOTPController(service, nil)

	qr := helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/api/users/target/2fas/totp/22/qr", nil), "store", store)
	qr = mux.SetURLVars(qr, map[string]string{"totp_id": "22"})
	qr = helpers.SetContextValue(qr, "user", &admin)
	qr = helpers.SetContextValue(qr, "_user", target)
	qr, qrAudit := withAuditRecorder(qr)
	controller.QR(httptest.NewRecorder(), qr)
	qrEvent := onlyEvent(t, qrAudit, audit.IAMMFAViewQR)
	assert.Equal(t, audit.UserActor(admin.ID, admin.Username, audit.AuthSession, ""), qrEvent.Actor)
	assert.Equal(t, audit.UserTarget(target.ID, target.Username), qrEvent.Event.Target)

	apiTokenQR := qr.WithContext(audit.WithActor(qr.Context(), audit.UserActor(admin.ID, admin.Username, audit.AuthAPIToken, "token-fingerprint")))
	apiTokenQR, apiTokenQRAudit := withAuditRecorder(apiTokenQR)
	controller.QR(httptest.NewRecorder(), apiTokenQR)
	apiTokenQREvent := onlyEvent(t, apiTokenQRAudit, audit.IAMMFAViewQR)
	assert.Equal(t, audit.UserActor(admin.ID, admin.Username, audit.AuthAPIToken, "token-fingerprint"), apiTokenQREvent.Actor)
	assert.Equal(t, audit.UserTarget(target.ID, target.Username), apiTokenQREvent.Event.Target)

	encoded, err := util.Cookie.Encode("semaphore", map[string]any{"user": admin.ID, "session": session.ID})
	require.NoError(t, err)
	acknowledge := helpers.SetContextValue(httptest.NewRequest(http.MethodPost,
		"/api/users/target/2fas/totp/22/recovery-codes/acknowledge", strings.NewReader(`{"stored":true}`)), "store", store)
	acknowledge.AddCookie(&http.Cookie{Name: "semaphore", Value: encoded})
	acknowledge = mux.SetURLVars(acknowledge, map[string]string{"totp_id": "22"})
	acknowledge = helpers.SetContextValue(acknowledge, "user", &admin)
	acknowledge = helpers.SetContextValue(acknowledge, "_user", target)
	acknowledge, acknowledgeAudit := withAuditRecorder(acknowledge)
	controller.AcknowledgeRecoveryCodes(httptest.NewRecorder(), acknowledge)
	acknowledgeEvent := onlyEvent(t, acknowledgeAudit, audit.IAMMFAEnable)
	assert.Equal(t, audit.UserActor(admin.ID, admin.Username, audit.AuthSession, ""), acknowledgeEvent.Actor)
	assert.Equal(t, audit.UserTarget(target.ID, target.Username), acknowledgeEvent.Event.Target)

}

func TestWriteTOTPErrorReturnsStableSecurityStatuses(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{pro_interfaces.ErrTOTPInvalidCode, http.StatusUnauthorized, "INVALID_PASSCODE"},
		{pro_interfaces.ErrTOTPReplay, http.StatusConflict, "TOTP_REPLAYED"},
		{pro_interfaces.ErrTOTPThrottled, http.StatusTooManyRequests, "TOTP_THROTTLED"},
		{pro_interfaces.ErrTOTPReadiness, http.StatusConflict, "TOTP_ADMIN_RECOVERY_NOT_READY"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		writeTOTPError(recorder, test.err)
		assert.Equal(t, test.status, recorder.Code)
		assert.Contains(t, recorder.Body.String(), test.code)
	}
}

func TestCreateSessionFailsClosedWhenTOTPIsUnavailable(t *testing.T) {
	service := &totpServiceStub{requirementErr: pro_interfaces.ErrTOTPUnavailable}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	recorder := httptest.NewRecorder()

	created := createSession(recorder, request, db.User{ID: 7}, false, service)

	assert.False(t, created)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "TOTP_UNAVAILABLE")
}

func TestTOTPControllerRecordsAllowlistedSecurityAudit(t *testing.T) {
	audit := &auditRecorderStub{}
	controller := NewTOTPController(&totpServiceStub{}, audit)
	request := httptest.NewRequest(http.MethodPost, "/api/users/4/2fas/totp",
		strings.NewReader(`{"reauthentication":"proof"}`))
	request = helpers.SetContextValue(request, "user", &db.User{ID: 4})
	request = helpers.SetContextValue(request, "_user", db.User{ID: 4})
	recorder := httptest.NewRecorder()
	controller.BeginEnrollment(recorder, request)
	require.Len(t, audit.events, 1)
	event := audit.events[0]
	assert.Equal(t, pro_interfaces.AuditActionTOTPEnrollBegin, event.Action)
	assert.Equal(t, string(pro_interfaces.CapabilityTOTP), event.TargetID)
	assert.Equal(t, pro_interfaces.AuditReasonEnrollmentPending, event.Reason)
	assert.NoError(t, event.Validate())
}

func TestTOTPControllerChallengeAndRecoveryContracts(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUser(db.UserWithPwd{Pwd: strings.Repeat("p", 14), User: db.User{
		Username: "totp-api", Name: "TOTP API", Email: "totp-api@example.test",
	}})
	require.NoError(t, err)
	session, err := store.CreateSession(db.Session{
		UserID: user.ID, VerificationMethod: db.SessionVerificationTotp,
		Created: time.Now().UTC(), LastActive: time.Now().UTC(),
	})
	require.NoError(t, err)

	service := &totpServiceStub{}
	controller := NewTOTPController(service, nil)

	verify, authAudit := withAuditRecorder(newTOTPAuthenticationRequest(t, store, session, "/api/auth/verify", `{"passcode":"123456"}`))
	verifyRecorder := httptest.NewRecorder()
	controller.VerifySession(verifyRecorder, verify)
	assert.Equal(t, http.StatusNoContent, verifyRecorder.Code)
	assert.Equal(t, "123456", service.verifiedCode)
	kinds, err := authAudit.Kinds()
	require.NoError(t, err)
	assert.Equal(t, []audit.Kind{audit.AuthMFAVerifyTOTP, audit.AuthLogin}, kinds)

	service.verifyError = pro_interfaces.ErrTOTPReplay
	replay := newTOTPAuthenticationRequest(t, store, session, "/api/auth/verify", `{"passcode":"123456"}`)
	replayRecorder := httptest.NewRecorder()
	controller.VerifySession(replayRecorder, replay)
	assert.Equal(t, http.StatusConflict, replayRecorder.Code)
	assert.Contains(t, replayRecorder.Body.String(), "TOTP_REPLAYED")

	service.verifyError = pro_interfaces.ErrTOTPInvalidCode
	invalidChallenge, challengeAudit := withAuditRecorder(newTOTPAuthenticationRequest(t, store, session, "/api/auth/verify", `{"passcode":"invalid"}`))
	invalidChallengeRecorder := httptest.NewRecorder()
	controller.VerifySession(invalidChallengeRecorder, invalidChallenge)
	assert.Equal(t, http.StatusUnauthorized, invalidChallengeRecorder.Code)
	got := onlyEvent(t, challengeAudit, audit.AuthMFAVerifyTOTP)
	assert.Equal(t, audit.OutcomeFailure, got.Event.Outcome)
	assert.Equal(t, audit.ReasonInvalidPasscode, got.Event.Reason)
	kinds, err = challengeAudit.Kinds()
	require.NoError(t, err)
	assert.NotContains(t, kinds, audit.AuthLogin)

	service.recoverError = pro_interfaces.ErrTOTPInvalidRecovery
	recovery, recoveryAudit := withAuditRecorder(newTOTPAuthenticationRequest(t, store, session, "/api/auth/recovery", `{"recovery_code":"RECOVERY"}`))
	recoveryRecorder := httptest.NewRecorder()
	controller.RecoverSession(recoveryRecorder, recovery)
	assert.Equal(t, http.StatusUnauthorized, recoveryRecorder.Code)
	assert.Equal(t, "RECOVERY", service.recoveryCode)
	assert.Contains(t, recoveryRecorder.Body.String(), "INVALID_RECOVERY_CODE")
	got = onlyEvent(t, recoveryAudit, audit.AuthMFARecover)
	assert.Equal(t, audit.OutcomeFailure, got.Event.Outcome)
	assert.Equal(t, audit.ReasonInvalidRecoveryCode, got.Event.Reason)
	kinds, err = recoveryAudit.Kinds()
	require.NoError(t, err)
	assert.NotContains(t, kinds, audit.AuthLogin)
}

func newTOTPAuthenticationRequest(
	t *testing.T,
	store db.Store,
	session db.Session,
	path string,
	body string,
) *http.Request {
	t.Helper()
	previousCookie := util.Cookie
	t.Cleanup(func() { util.Cookie = previousCookie })
	util.Cookie = securecookie.New(
		[]byte(strings.Repeat("h", 64)),
		[]byte(strings.Repeat("e", 32)),
	)
	encoded, err := util.Cookie.Encode("semaphore", map[string]any{
		"user": session.UserID, "session": session.ID,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: "semaphore", Value: encoded})
	return helpers.SetContextValue(request, "store", store)
}

var _ pro_interfaces.TOTPService = (*totpServiceStub)(nil)
