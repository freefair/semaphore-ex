package features

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchingTOTPStepUsesRFC6238BoundedWindow(t *testing.T) {
	const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	step, valid, err := matchingTOTPStep(rfcSecret, "287082", time.Unix(59, 0).UTC())
	require.NoError(t, err)
	assert.True(t, valid)
	assert.Equal(t, int64(1), step)

	_, valid, err = matchingTOTPStep(rfcSecret, "287082", time.Unix(60, 0).UTC())
	require.NoError(t, err)
	assert.True(t, valid, "one previous 30-second time step is accepted")

	_, valid, err = matchingTOTPStep(rfcSecret, "287082", time.Unix(90, 0).UTC())
	require.NoError(t, err)
	assert.False(t, valid, "two previous time steps exceed the bounded skew")
}

func TestTOTPEnrollmentRequiresConfirmationAndAcknowledgement(t *testing.T) {
	store, service, user, credential, now := newTOTPTestService(t)
	defer store.Close()

	ceremony, err := service.BeginEnrollment(context.Background(), pro_interfaces.TOTPEnrollmentRequest{
		ActorID: user.ID, TargetUserID: user.ID, Reauthentication: credential, Now: now,
	})
	require.NoError(t, err)
	require.Len(t, ceremony.RecoveryCodes, totpRecoveryCodeCount)

	persisted, err := store.GetTOTP(user.ID)
	require.NoError(t, err)
	assert.Equal(t, string(pro_interfaces.TOTPEnrollmentPendingConfirmation), persisted.State)
	assert.Empty(t, persisted.URL)
	assert.NotEqual(t, ceremony.ProvisioningURI, persisted.EncryptedSecret)
	assert.NotContains(t, persisted.EncryptedSecret, "otpauth://")

	status, err := service.Status(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.TOTPEnrollmentPendingConfirmation, status.EnrollmentState)

	key, err := otp.NewKeyFromURL(ceremony.ProvisioningURI)
	require.NoError(t, err)
	code, err := totp.GenerateCode(key.Secret(), now)
	require.NoError(t, err)
	status, err = service.ConfirmEnrollment(context.Background(), pro_interfaces.TOTPConfirmationRequest{
		ActorID: user.ID, TargetUserID: user.ID, EnrollmentID: ceremony.ID,
		Reauthentication: credential, Passcode: code, Now: now,
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.TOTPEnrollmentPendingRecoveryAck, status.EnrollmentState)

	current, err := store.CreateSession(db.Session{UserID: user.ID, Created: now, LastActive: now})
	require.NoError(t, err)
	prior, err := store.CreateSession(db.Session{UserID: user.ID, Created: now, LastActive: now})
	require.NoError(t, err)
	_, err = service.AcknowledgeRecoveryCodes(context.Background(), pro_interfaces.TOTPRecoveryAcknowledgement{
		ActorID: user.ID, TargetUserID: user.ID, EnrollmentID: ceremony.ID,
		SessionID: current.ID, Stored: false, Now: now,
	})
	require.Error(t, err)

	_, err = service.AcknowledgeRecoveryCodes(context.Background(), pro_interfaces.TOTPRecoveryAcknowledgement{
		ActorID: user.ID, TargetUserID: user.ID, EnrollmentID: ceremony.ID,
		SessionID: current.ID + prior.ID + 1000, Stored: true, Now: now,
	})
	require.Error(t, err)
	persisted, err = store.GetTOTP(user.ID)
	require.NoError(t, err)
	assert.Equal(t, string(pro_interfaces.TOTPEnrollmentPendingRecoveryAck), persisted.State)
	_, err = store.GetSession(user.ID, prior.ID)
	require.NoError(t, err, "session revocation must roll back when activation cannot verify the current session")

	status, err = service.AcknowledgeRecoveryCodes(context.Background(), pro_interfaces.TOTPRecoveryAcknowledgement{
		ActorID: user.ID, TargetUserID: user.ID, EnrollmentID: ceremony.ID,
		SessionID: current.ID, Stored: true, Now: now,
	})
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.TOTPEnrollmentActive, status.EnrollmentState)
	assert.True(t, status.RecoveryAcknowledged)
	assert.Equal(t, totpRecoveryCodeCount, status.RecoveryCodesRemaining)
	_, err = store.GetSession(user.ID, prior.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetSession(user.ID, current.ID)
	require.NoError(t, err)
}

func TestTOTPInitializationPersistsFailClosedPolicyBeforeLegacyEncryption(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	now := time.Unix(1_700_000_000, 0).UTC()
	user, err := store.CreateUser(db.UserWithPwd{Pwd: strings.Repeat("p", 14), User: db.User{
		Username: "legacy-totp", Name: "Legacy TOTP", Email: "legacy-totp@example.test",
	}})
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		"insert into user__totp (user_id, url, recovery_hash, created) values (?, ?, ?, ?)",
		user.ID, "otpauth://totp/Semaphore:legacy?secret=LEGACY", "legacy-recovery-hash", now,
	)
	require.NoError(t, err)

	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	util.Config = &util.ConfigType{}
	service := NewTOTPService(store, NewCapabilityProvider(store))
	service.(*totpService).now = func() time.Time { return now }

	err = service.Initialize(context.Background())
	require.ErrorContains(t, err, "requires option or access-key encryption")
	configuration, configErr := store.GetCapabilityConfig(string(pro_interfaces.CapabilityTOTP))
	require.NoError(t, configErr)
	assert.Equal(t, string(pro_interfaces.CapabilityStateOptional), configuration.State)
	persisted, persistedErr := store.GetTOTP(user.ID)
	require.NoError(t, persistedErr)
	assert.Contains(t, persisted.URL, "otpauth://", "startup failure must leave the legacy row unchanged")
	assert.Empty(t, persisted.EncryptedSecret)
}

func TestTOTPStepAndRecoveryCodeAreSingleUse(t *testing.T) {
	store, service, user, credential, now := newTOTPTestService(t)
	defer store.Close()
	ceremony, key := activateTOTP(t, store, service, user, credential, now)

	firstSession, err := store.CreateSession(db.Session{
		UserID: user.ID, Created: now, LastActive: now,
		VerificationMethod: db.SessionVerificationTotp,
	})
	require.NoError(t, err)
	secondSession, err := store.CreateSession(db.Session{
		UserID: user.ID, Created: now, LastActive: now,
		VerificationMethod: db.SessionVerificationTotp,
	})
	require.NoError(t, err)
	challengeAt := now.Add(totpPeriod)
	code, err := totp.GenerateCode(key.Secret(), challengeAt)
	require.NoError(t, err)

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, sessionID := range []int{firstSession.ID, secondSession.ID} {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			results <- service.VerifyChallenge(context.Background(), user.ID, id, code, challengeAt)
		}(sessionID)
	}
	wait.Wait()
	close(results)
	var succeeded, replayed int
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, pro_interfaces.ErrTOTPReplay):
			replayed++
		default:
			t.Fatalf("unexpected concurrent challenge result: %v", result)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, replayed)

	recoverySession, err := store.CreateSession(db.Session{
		UserID: user.ID, Created: now, LastActive: now,
		VerificationMethod: db.SessionVerificationTotp,
	})
	require.NoError(t, err)
	recoveryCode := ceremony.RecoveryCodes[0]
	require.NoError(t, service.RecoverSession(
		context.Background(), user.ID, recoverySession.ID, recoveryCode, challengeAt,
	))
	err = service.RecoverSession(context.Background(), user.ID, recoverySession.ID, recoveryCode, challengeAt)
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPInvalidRecovery)
}

func TestTOTPThrottlingAndRequiredModeReadiness(t *testing.T) {
	store, service, user, credential, now := newTOTPTestService(t)
	defer store.Close()
	_, key := activateTOTP(t, store, service, user, credential, now)

	session, err := store.CreateSession(db.Session{
		UserID: user.ID, Created: now, LastActive: now,
		VerificationMethod: db.SessionVerificationTotp,
	})
	require.NoError(t, err)
	for attempt := 1; attempt < totpMaxFailures; attempt++ {
		err = service.VerifyChallenge(context.Background(), user.ID, session.ID, "000000", now)
		assert.ErrorIs(t, err, pro_interfaces.ErrTOTPInvalidCode)
	}
	err = service.VerifyChallenge(context.Background(), user.ID, session.ID, "000000", now)
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPThrottled)
	validCode, err := totp.GenerateCode(key.Secret(), now.Add(totpPeriod))
	require.NoError(t, err)
	err = service.VerifyChallenge(context.Background(), user.ID, session.ID, validCode, now.Add(time.Minute))
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPThrottled)

	_, err = service.Configure(context.Background(), pro_interfaces.TOTPConfigurationRequest{
		ActorID: user.ID, ActorIsAdmin: true, State: pro_interfaces.CapabilityStateRequired, Now: now,
	})
	require.NoError(t, err, "an active local admin with acknowledged recovery codes satisfies readiness")
	transitions, err := service.Transitions(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, transitions)
	assert.Equal(t, string(pro_interfaces.CapabilityStateRequired), transitions[0].ToState)

	err = service.ResetEnrollment(context.Background(), pro_interfaces.TOTPResetRequest{
		ActorID: user.ID, ActorIsAdmin: true, TargetUserID: user.ID,
		EnrollmentID: mustTOTP(t, store, user.ID).ID, Reauthentication: credential, Now: now,
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPReadiness)
}

func TestRequiredModeRejectedWithoutRecoverableAdmin(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	configureTOTPEncryption(t)
	credential := strings.Repeat("c", 14)
	admin, err := store.CreateUser(db.UserWithPwd{Pwd: credential, User: db.User{
		Username: "admin-unready", Name: "Unready Admin", Email: "unready@example.test", Admin: true,
	}})
	require.NoError(t, err)
	provider := NewCapabilityProvider(store)
	service := NewTOTPService(store, provider)
	_, err = service.Configure(context.Background(), pro_interfaces.TOTPConfigurationRequest{
		ActorID: admin.ID, ActorIsAdmin: true, State: pro_interfaces.CapabilityStateRequired,
		Now: time.Unix(1_700_000_000, 0).UTC(),
	})
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPReadiness)
}

func TestRequiredModeFailsClosedForExternalUserCreatedAfterEnablement(t *testing.T) {
	store, service, admin, credential, now := newTOTPTestService(t)
	defer store.Close()
	activateTOTP(t, store, service, admin, credential, now)
	_, err := service.Configure(context.Background(), pro_interfaces.TOTPConfigurationRequest{
		ActorID: admin.ID, ActorIsAdmin: true, State: pro_interfaces.CapabilityStateRequired, Now: now,
	})
	require.NoError(t, err)

	external, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "external-after-required", Name: "External User",
		Email: "external-after-required@example.test", External: true,
	}})
	require.NoError(t, err)

	_, err = service.SessionRequirement(context.Background(), external.ID)
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPForbidden)
}

func TestConcurrentRequiredAdminResetsPreserveOneRecoverableAdministrator(t *testing.T) {
	store, service, first, firstCredential, now := newTOTPTestService(t)
	defer store.Close()
	activateTOTP(t, store, service, first, firstCredential, now)
	secondCredential := strings.Repeat("s", 14)
	second, err := store.CreateUser(db.UserWithPwd{Pwd: secondCredential, User: db.User{
		Username: "totp-admin-second", Name: "Second TOTP Admin",
		Email: "totp-admin-second@example.test", Admin: true,
	}})
	require.NoError(t, err)
	activateTOTP(t, store, service, second, secondCredential, now)
	_, err = service.Configure(context.Background(), pro_interfaces.TOTPConfigurationRequest{
		ActorID: first.ID, ActorIsAdmin: true, State: pro_interfaces.CapabilityStateRequired, Now: now,
	})
	require.NoError(t, err)

	requests := []pro_interfaces.TOTPResetRequest{
		{
			ActorID: first.ID, ActorIsAdmin: true, TargetUserID: first.ID,
			EnrollmentID: mustTOTP(t, store, first.ID).ID, Reauthentication: firstCredential, Now: now,
		},
		{
			ActorID: second.ID, ActorIsAdmin: true, TargetUserID: second.ID,
			EnrollmentID: mustTOTP(t, store, second.ID).ID, Reauthentication: secondCredential, Now: now,
		},
	}
	results := make(chan error, len(requests))
	var wait sync.WaitGroup
	for _, request := range requests {
		wait.Add(1)
		go func(current pro_interfaces.TOTPResetRequest) {
			defer wait.Done()
			results <- service.ResetEnrollment(context.Background(), current)
		}(request)
	}
	wait.Wait()
	close(results)

	var reset, protected int
	for result := range results {
		switch {
		case result == nil:
			reset++
		case errors.Is(result, pro_interfaces.ErrTOTPReadiness):
			protected++
		default:
			t.Fatalf("unexpected concurrent reset result: %v", result)
		}
	}
	assert.Equal(t, 1, reset)
	assert.Equal(t, 1, protected)
	recoverable, err := store.CountRecoverableTOTPAdmins(0)
	require.NoError(t, err)
	assert.Equal(t, 1, recoverable)
}

func newTOTPTestService(
	t *testing.T,
) (*sqldb.SqlDb, pro_interfaces.TOTPService, db.User, string, time.Time) {
	t.Helper()
	store := sqldb.InitConfigCreateTestStore()
	configureTOTPEncryption(t)
	credential := strings.Repeat("t", 14)
	user, err := store.CreateUser(db.UserWithPwd{Pwd: credential, User: db.User{
		Username: "totp-admin", Name: "TOTP Admin", Email: "totp@example.test", Admin: true,
	}})
	require.NoError(t, err)
	now := time.Unix(1_700_000_000, 0).UTC()
	require.NoError(t, store.ConfigureTOTP(
		string(pro_interfaces.CapabilityStateOptional), nil, user.ID, now,
	))
	provider := NewCapabilityProvider(store)
	service := NewTOTPService(store, provider)
	service.(*totpService).now = func() time.Time { return now }
	return store, service, user, credential, now
}

func configureTOTPEncryption(t *testing.T) {
	t.Helper()
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func activateTOTP(
	t *testing.T,
	store *sqldb.SqlDb,
	service pro_interfaces.TOTPService,
	user db.User,
	credential string,
	now time.Time,
) (pro_interfaces.TOTPEnrollmentCeremony, *otp.Key) {
	t.Helper()
	ceremony, err := service.BeginEnrollment(context.Background(), pro_interfaces.TOTPEnrollmentRequest{
		ActorID: user.ID, TargetUserID: user.ID, Reauthentication: credential, Now: now,
	})
	require.NoError(t, err)
	key, err := otp.NewKeyFromURL(ceremony.ProvisioningURI)
	require.NoError(t, err)
	code, err := totp.GenerateCode(key.Secret(), now)
	require.NoError(t, err)
	_, err = service.ConfirmEnrollment(context.Background(), pro_interfaces.TOTPConfirmationRequest{
		ActorID: user.ID, TargetUserID: user.ID, EnrollmentID: ceremony.ID,
		Reauthentication: credential, Passcode: code, Now: now,
	})
	require.NoError(t, err)
	session, err := store.CreateSession(db.Session{UserID: user.ID, Created: now, LastActive: now})
	require.NoError(t, err)
	_, err = service.AcknowledgeRecoveryCodes(context.Background(), pro_interfaces.TOTPRecoveryAcknowledgement{
		ActorID: user.ID, TargetUserID: user.ID, EnrollmentID: ceremony.ID,
		SessionID: session.ID, Stored: true, Now: now,
	})
	require.NoError(t, err)
	return ceremony, key
}

func mustTOTP(t *testing.T, store *sqldb.SqlDb, userID int) db.UserTotp {
	t.Helper()
	enrollment, err := store.GetTOTP(userID)
	require.NoError(t, err)
	return enrollment
}
