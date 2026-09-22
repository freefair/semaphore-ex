package sql

import (
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assertCredentialBooleanMigration(t testing.TB, store *SqlDb) {
	t.Helper()
	now := time.Now().UTC()
	owner, err := store.CreateUserWithoutPassword(db.User{Username: "boolean-owner", Name: "Boolean owner", Email: "boolean@example.test"})
	require.NoError(t, err)
	project, err := store.CreateProject(db.Project{Name: "Boolean credentials"})
	require.NoError(t, err)
	var records []db.GlobalCredential
	for _, enabled := range []bool{false, true} {
		record, _, err := store.CreateGlobalCredential(db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Boolean", OwnerUserID: owner.ID, Enabled: enabled, Created: now}, db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "test-fixture", CreatedByUserID: owner.ID})
		require.NoError(t, err)
		records = append(records, record)
		_, err = store.CreateGlobalCredentialGrant(db.GlobalCredentialGrant{CredentialID: record.ID, ProjectID: project.ID, Operations: db.GlobalCredentialGrantOperationReference, Status: db.GlobalCredentialGrantStatusActive, CreatedByUserID: owner.ID, Created: now})
		require.NoError(t, err)
	}
	visible, err := store.GetEffectiveGlobalCredentialMetadata(project.ID, now, db.RetrieveQueryParams{Count: 100})
	require.NoError(t, err)
	require.Len(t, visible, 1)
	assert.Equal(t, records[1].ID, visible[0].CredentialID)

	if store.GetDialect() == util.DbDriverPostgres {
		var column struct {
			Type     string `db:"data_type"`
			Nullable string `db:"is_nullable"`
			Default  string `db:"column_default"`
		}
		require.NoError(t, store.selectOne(&column, "select data_type,is_nullable,column_default from information_schema.columns where table_schema=current_schema() and table_name='global_credential' and column_name='enabled'"))
		assert.Equal(t, "boolean", column.Type)
		assert.Equal(t, "NO", column.Nullable)
		assert.Equal(t, "true", column.Default)
	}
	require.NoError(t, store.TryRollbackMigration(db.Migration{Version: "2.20.5-ex1.4"}))
	for i, record := range records {
		value, err := store.Sql().SelectInt(store.PrepareQuery("select enabled from global_credential where id=?"), record.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(i), value)
	}
	if store.GetDialect() == util.DbDriverPostgres {
		_, err = store.exec("update global_credential set enabled=2 where id=?", records[0].ID)
		require.NoError(t, err)
		require.ErrorContains(t, store.ApplyMigration(db.Migration{Version: "2.20.5-ex1.4"}), "invalid input syntax for type boolean")
		applied, err := store.IsMigrationApplied(db.Migration{Version: "2.20.5-ex1.4"})
		require.NoError(t, err)
		assert.False(t, applied)
		value, err := store.Sql().SelectInt(store.PrepareQuery("select enabled from global_credential where id=?"), records[0].ID)
		require.NoError(t, err)
		assert.Equal(t, int64(2), value)
		_, err = store.exec("update global_credential set enabled=0 where id=?", records[0].ID)
		require.NoError(t, err)
	}
	require.NoError(t, store.ApplyMigration(db.Migration{Version: "2.20.5-ex1.4"}))
	for i, record := range records {
		loaded, err := store.GetGlobalCredential(record.ID)
		require.NoError(t, err)
		assert.Equal(t, i == 1, loaded.Enabled)
	}
	_, err = store.SetGlobalCredentialEnabled(records[1].ID, false, records[1].Revision, now)
	require.NoError(t, err)
	visible, err = store.GetEffectiveGlobalCredentialMetadata(project.ID, now, db.RetrieveQueryParams{Count: 100})
	require.NoError(t, err)
	assert.Empty(t, visible)
	deletable, _, err := store.CreateGlobalCredential(db.GlobalCredential{Type: db.GlobalCredentialTypeString, DisplayName: "Deletable", OwnerUserID: owner.ID, Enabled: true, Created: now}, db.GlobalCredentialVersion{MaterialKind: db.GlobalCredentialMaterialLocalEncrypted, EncryptedMaterial: "test-fixture", CreatedByUserID: owner.ID})
	require.NoError(t, err)
	disabled, err := store.SetGlobalCredentialEnabled(deletable.ID, false, deletable.Revision, now)
	require.NoError(t, err)
	require.NoError(t, store.DeleteGlobalCredential(deletable.ID, disabled.Revision))
}

func TestCredentialBooleanMigrationSQLite(t *testing.T) {
	store := InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	assertCredentialBooleanMigration(t, store)
}
