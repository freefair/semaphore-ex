package features

import (
	"context"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityTOTPRequirementFailsClosedForExistingEnrollment(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	previousMFA := util.Config.Mfa
	t.Cleanup(func() { util.Config.Mfa = previousMFA })
	util.Config.Mfa = &util.MultifactorAuthConfig{Totp: &util.TotpConfig{Enabled: false}}
	user, err := store.CreateUser(db.UserWithPwd{Pwd: "long-enough-password", User: db.User{
		Username: "legacy-totp", Name: "Legacy TOTP", Email: "legacy-totp@example.test",
	}})
	require.NoError(t, err)
	_, err = store.Sql().Exec(
		`insert into user__totp
		 (user_id, url, recovery_hash, encrypted_secret, state, created)
		 values (?, 'otpauth://legacy', '', '', 'active', ?)`,
		user.ID, time.Now().UTC(),
	)
	require.NoError(t, err)

	service := NewTOTPService(store, NewCapabilityProvider(store))
	requirement, err := service.SessionRequirement(context.Background(), user.ID)
	assert.Equal(t, pro_interfaces.TOTPSessionNone, requirement)
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPUnavailable)

	util.Config.Mfa = nil
	requirement, err = service.SessionRequirement(context.Background(), user.ID)
	assert.Equal(t, pro_interfaces.TOTPSessionNone, requirement)
	assert.ErrorIs(t, err, pro_interfaces.ErrTOTPUnavailable)
}

func TestCommunityTOTPRequirementLeavesUsersWithoutEnrollmentUnchanged(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	user, err := store.CreateUser(db.UserWithPwd{Pwd: "long-enough-password", User: db.User{
		Username: "without-totp", Name: "Without TOTP", Email: "without-totp@example.test",
	}})
	require.NoError(t, err)

	service := NewTOTPService(store, NewCapabilityProvider(store))
	requirement, err := service.SessionRequirement(context.Background(), user.ID)
	require.NoError(t, err)
	assert.Equal(t, pro_interfaces.TOTPSessionNone, requirement)
}
