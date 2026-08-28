package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigureLDAPProviderRequiresCurrentRecoveryAdmin(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	now := time.Unix(1_700_000_000, 0).UTC()
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, nil)))

	err := store.ConfigureLDAPProvider("corp", "active", nil, 1, now, 15*time.Minute)

	assert.ErrorIs(t, err, db.ErrLDAPReadiness)
}

func TestConfigureLDAPProviderRejectsFutureReadiness(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	now := time.Unix(1_700_000_000, 0).UTC()
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "recovery-admin", Name: "Recovery Admin", Email: "recovery@example.test", Admin: true,
	}, Pwd: "local-recovery-password"})
	require.NoError(t, err)
	future := now.Add(time.Minute)
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", future, &admin.ID)))

	err = store.ConfigureLDAPProvider("corp", "active", nil, admin.ID, now, 15*time.Minute)

	assert.ErrorIs(t, err, db.ErrLDAPReadiness)
}

func TestConfigureLDAPProviderPersistsSelectionOnlyForSelectedState(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	now := time.Unix(1_700_000_000, 0).UTC()
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "recovery-admin", Name: "Recovery Admin", Email: "recovery@example.test", Admin: true,
	}, Pwd: "local-recovery-password"})
	require.NoError(t, err)
	ldapUser, err := store.CreateUserWithoutPassword(db.User{
		Username: "ldap-user", Name: "LDAP User", Email: "ldap@example.test", External: true,
	})
	require.NoError(t, err)
	_, err = store.CreateExternalIdentity(db.UserExternalIdentity{
		UserID: ldapUser.ID, Type: db.IdentityTypeLdap, Provider: "corp", ExternalUID: "id-1",
	})
	require.NoError(t, err)
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, &admin.ID)))

	require.NoError(t, store.ConfigureLDAPProvider(
		"corp", "selected_users", []int{ldapUser.ID}, admin.ID, now, 15*time.Minute,
	))
	require.NoError(t, store.ConfigureLDAPProvider(
		"corp", "active", []int{ldapUser.ID}, admin.ID, now.Add(time.Second), 15*time.Minute,
	))

	selected, err := store.GetLDAPSelectedUsers("corp")
	require.NoError(t, err)
	assert.Empty(t, selected)
	transitions, err := store.GetLDAPCapabilityTransitions("corp")
	require.NoError(t, err)
	require.Len(t, transitions, 2)
	assert.Equal(t, "selected_users", transitions[0].FromState)
	assert.Equal(t, "active", transitions[0].ToState)
}

func TestSaveLDAPReadinessRejectsStaleProviderVersion(t *testing.T) {
	store := InitConfigCreateTestStore()
	defer store.Close()
	testedVersion := time.Unix(1_700_000_000, 0).UTC()
	provider := readyLDAPProvider("corp", testedVersion, nil)
	provider.ReadinessStatus = "untested"
	provider.ReadinessCode = "configuration_changed"
	provider.ReadinessCheckedAt = nil
	provider.RecoveryCheckedAt = nil
	require.NoError(t, store.SaveLDAPProvider(provider))

	provider.ServerURL = "ldaps://replacement.example.test:636"
	provider.Updated = testedVersion.Add(time.Minute)
	require.NoError(t, store.SaveLDAPProvider(provider))

	err := store.SaveLDAPReadiness("corp", "ready", "ready", testedVersion.Add(2*time.Minute), nil, 1)

	assert.ErrorIs(t, err, db.ErrLDAPReadiness)
	stored, err := store.GetLDAPProvider("corp")
	require.NoError(t, err)
	assert.Equal(t, "untested", stored.ReadinessStatus)
	assert.Equal(t, provider.ServerURL, stored.ServerURL)
	assert.Equal(t, 2, stored.ConfigVersion)
}

func readyLDAPProvider(id string, checkedAt time.Time, recoveryAdminUserID *int) db.LDAPProvider {
	return db.LDAPProvider{
		ID: id, DisplayName: "Corporate LDAP", State: "disabled", ServerURL: "ldaps://ldap.example.test:636",
		TLSMode: "ldaps", TrustMode: "system", BindDN: "cn=bind,dc=example,dc=test",
		EncryptedBindPassword: "encrypted", SearchBaseDN: "ou=users,dc=example,dc=test",
		UserFilter: "(uid={{username}})", IdentityAttribute: "entryUUID", UsernameAttribute: "uid",
		NameAttribute: "cn", EmailAttribute: "mail", ReadinessStatus: "ready", ReadinessCode: "ready",
		ReadinessCheckedAt: &checkedAt, RecoveryAdminUserID: recoveryAdminUserID,
		RecoveryCheckedAt: &checkedAt, Created: checkedAt, Updated: checkedAt,
	}
}
