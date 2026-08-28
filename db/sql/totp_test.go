package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTOTPRequiredReadinessIsEnforcedInsideRepositoryTransactions(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC()

	err := store.ConfigureTOTP("required", nil, 1, now)
	assert.ErrorIs(t, err, db.ErrTOTPReadiness)

	admin, err := store.CreateUser(db.UserWithPwd{Pwd: "long-enough-password", User: db.User{
		Username: "ready-admin", Name: "Ready Admin", Email: "ready-admin@example.test", Admin: true,
	}})
	require.NoError(t, err)
	enrollment, err := store.CreateTOTPEnrollment(db.UserTotp{
		UserID: admin.ID, EncryptedSecret: "encrypted", State: "active",
		ConfirmedAt: &now, RecoveryAcknowledgedAt: &now, Created: now,
	}, []string{"hashed-recovery-code"})
	require.NoError(t, err)
	session, err := store.CreateSession(db.Session{
		UserID: admin.ID, Created: now, LastActive: now, Verified: true,
	})
	require.NoError(t, err)

	require.NoError(t, store.ConfigureTOTP("required", nil, admin.ID, now))
	err = store.ResetTOTPEnrollmentAndRevokeSessions(admin.ID, enrollment.ID)
	assert.ErrorIs(t, err, db.ErrTOTPReadiness)
	_, err = store.GetTOTP(admin.ID)
	require.NoError(t, err)
	_, err = store.GetSession(admin.ID, session.ID)
	require.NoError(t, err)
}

func TestTOTPHostRecoveryForceResetBypassesReadinessAndRevokesSessions(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	now := time.Now().UTC()

	admin, err := store.CreateUser(db.UserWithPwd{Pwd: "long-enough-password", User: db.User{
		Username: "locked-admin", Name: "Locked Admin", Email: "locked-admin@example.test", Admin: true,
	}})
	require.NoError(t, err)
	enrollment, err := store.CreateTOTPEnrollment(db.UserTotp{
		UserID: admin.ID, EncryptedSecret: "encrypted", State: "active",
		ConfirmedAt: &now, RecoveryAcknowledgedAt: &now, Created: now,
	}, []string{"hashed-recovery-code"})
	require.NoError(t, err)
	session, err := store.CreateSession(db.Session{
		UserID: admin.ID, Created: now, LastActive: now, Verified: true,
	})
	require.NoError(t, err)
	require.NoError(t, store.ConfigureTOTP("required", nil, admin.ID, now))

	require.NoError(t, store.ForceResetTOTPEnrollmentAndRevokeSessions(admin.ID, enrollment.ID))
	_, err = store.GetTOTP(admin.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetSession(admin.ID, session.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
}
