package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// D1: directory login may only be exposed when at least one enabled group
// mapping decides who gets access.
func TestConfigureLDAPProviderRequiresEnabledGroupMapping(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_700_000_000, 0).UTC()
	admin, err := store.CreateUser(db.UserWithPwd{User: db.User{
		Username: "recovery-admin", Name: "Recovery Admin", Email: "recovery@example.test", Admin: true,
	}, Pwd: "local-recovery-password"})
	require.NoError(t, err)
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, &admin.ID)))
	auditor, err := store.CreateGlobalRole(db.Role{
		ID: "access_auditor", Name: "Access auditor", GlobalPermissions: db.CanReadGlobalAudit, Revision: 1,
	})
	require.NoError(t, err)

	err = store.ConfigureLDAPProvider("corp", "active", nil, admin.ID, now, 15*time.Minute)
	assert.ErrorIs(t, err, db.ErrLDAPNoEnabledGroupMapping, "no mapping at all")

	mapping, err := store.SaveLDAPGroupMapping(db.LDAPGroupMapping{
		ID: "auditors", ProviderID: "corp", GroupExternalID: "entryuuid:40112233-4455-6677-8899-aabbccddeeff",
		TargetScope: "global", RoleID: string(auditor.ID), Enabled: false, Revision: 1, Created: now, Updated: now,
	}, 0)
	require.NoError(t, err)
	err = store.ConfigureLDAPProvider("corp", "active", nil, admin.ID, now, 15*time.Minute)
	assert.ErrorIs(t, err, db.ErrLDAPNoEnabledGroupMapping, "only a disabled mapping")

	mapping.Enabled = true
	mapping.Updated = now.Add(time.Second)
	_, err = store.SaveLDAPGroupMapping(mapping, mapping.Revision)
	require.NoError(t, err)
	assert.NoError(t, store.ConfigureLDAPProvider("corp", "active", nil, admin.ID, now, 15*time.Minute))
}

func TestApplyLDAPGroupPreviewAndRevokeExpiresSessionsOfListedUsersOnly(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Unix(1_780_000_000, 0).UTC()
	require.NoError(t, store.SaveLDAPProvider(readyLDAPProvider("corp", now, nil)))
	lost, err := store.CreateUserWithoutPassword(db.User{
		Username: "lost-access", Name: "Lost access", Email: "lost@example.test", External: true,
	})
	require.NoError(t, err)
	kept, err := store.CreateUserWithoutPassword(db.User{
		Username: "kept-access", Name: "Kept access", Email: "kept@example.test", External: true,
	})
	require.NoError(t, err)
	lostSession, err := store.CreateSession(db.Session{UserID: lost.ID, Created: now, LastActive: now})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "lost-access-api-key", UserID: lost.ID, Name: "lost access"})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "kept-access-api-key", UserID: kept.ID, Name: "kept access"})
	require.NoError(t, err)
	keptSession, err := store.CreateSession(db.Session{UserID: kept.ID, Created: now, LastActive: now})
	require.NoError(t, err)
	revision, err := store.GetLDAPGroupMappingRevision("corp")
	require.NoError(t, err)
	preview := db.LDAPGroupReconciliation{
		ProviderID: "corp", Source: "manual", Status: "preview", Token: "revoke-one",
		MappingRevision: revision, DirectoryRevision: "directory-one", PreviewJSON: "{}", Created: now,
	}
	_, err = store.SaveLDAPGroupReconciliation(preview)
	require.NoError(t, err)
	appliedAt := now.Add(time.Second)
	preview.Status = "applied"
	preview.AppliedAt = &appliedAt

	_, err = store.ApplyLDAPGroupPreviewAndRevoke(preview, nil, nil, []int{lost.ID})
	require.NoError(t, err)

	sessionExpired := func(sessionID int) bool {
		var expired bool
		require.NoError(t, store.Sql().QueryRow("select expired from session where id=?", sessionID).Scan(&expired))
		return expired
	}
	assert.True(t, sessionExpired(lostSession.ID))
	assert.False(t, sessionExpired(keptSession.ID))
	keyExpired := func(keyID string) bool {
		var expired bool
		require.NoError(t, store.Sql().QueryRow("select expired from user__token where id=?", keyID).Scan(&expired))
		return expired
	}
	assert.True(t, keyExpired("lost-access-api-key"))
	assert.False(t, keyExpired("kept-access-api-key"))
	// GetSession serves only live sessions.
	_, err = store.GetSession(lost.ID, lostSession.ID)
	assert.Error(t, err)
	_, err = store.GetSession(kept.ID, keptSession.ID)
	assert.NoError(t, err)
}
