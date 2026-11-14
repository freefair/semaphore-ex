package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasswordChangeAndSessionRevocationKeepsOnlyCurrentSession(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	user, err := store.CreateUser(db.UserWithPwd{Pwd: "original-password", User: db.User{
		Username: "password-revocation", Name: "Password Revocation", Email: "password-revocation@example.test",
	}})
	require.NoError(t, err)
	now := time.Now().UTC()
	current, err := store.CreateSession(db.Session{UserID: user.ID, Created: now, LastActive: now, Verified: true})
	require.NoError(t, err)
	other, err := store.CreateSession(db.Session{UserID: user.ID, Created: now, LastActive: now, Verified: true})
	require.NoError(t, err)

	require.NoError(t, store.SetUserPasswordAndRevokeSessions(user.ID, "replacement-password", current.ID))
	_, err = store.GetSession(user.ID, current.ID)
	require.NoError(t, err)
	_, err = store.GetSession(user.ID, other.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
}

func TestSetUserDisabledAndRevokeCredentialsDisablesAuthentication(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	user, err := store.CreateUser(db.UserWithPwd{Pwd: "original-password", User: db.User{
		Username: "deactivated-user", Name: "Deactivated User", Email: "deactivated-user@example.test",
	}})
	require.NoError(t, err)
	now := time.Now().UTC()
	session, err := store.CreateSession(db.Session{UserID: user.ID, Created: now, LastActive: now, Verified: true})
	require.NoError(t, err)
	token, err := store.CreateAPIToken(db.APIToken{ID: "deactivated-token", UserID: user.ID, Name: "deactivation"})
	require.NoError(t, err)

	require.NoError(t, store.SetUserDisabledAndRevokeCredentials(user.ID, true))
	persisted, err := store.GetUser(user.ID)
	require.NoError(t, err)
	assert.True(t, persisted.Disabled)
	_, err = store.GetSession(user.ID, session.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetAPIToken(token.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	_, err = store.GetUserByLoginOrEmail(user.Username, user.Email)
	assert.ErrorIs(t, err, db.ErrNotFound)

	require.NoError(t, store.SetUserDisabledAndRevokeCredentials(user.ID, false))
	persisted, err = store.GetUser(user.ID)
	require.NoError(t, err)
	assert.False(t, persisted.Disabled)
	_, err = store.GetUserByLoginOrEmail(user.Username, user.Email)
	require.NoError(t, err)
}

func TestSetUserDisabledAndRevokeCredentialsKeepsAnEffectiveAdministrator(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	admin, err := store.CreateUser(db.UserWithPwd{Pwd: "original-password", User: db.User{
		Username: "last-admin", Name: "Last Admin", Email: "last-admin@example.test", Admin: true,
	}})
	require.NoError(t, err)

	err = store.SetUserDisabledAndRevokeCredentials(admin.ID, true)
	assert.ErrorIs(t, err, db.ErrLastGlobalAdministrator)
	persisted, err := store.GetUser(admin.ID)
	require.NoError(t, err)
	assert.False(t, persisted.Disabled)
}
