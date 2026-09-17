package sql

import (
	"encoding/base64"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerraformStateStoreKeepsEncryptedVersionsAndFencesLocks(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString(make([]byte, 32))
	project, err := store.CreateProject(db.Project{Name: "Terraform state"})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Name: "state", Type: db.InventoryTerraformWorkspace})
	require.NoError(t, err)
	backend := NewTerraformStore(store.GetConnection())
	secondBackend := NewTerraformStore(store.GetConnection())
	ciphertext, err := util.Config.EncryptAccessSecret([]byte(`{"version":4}`))
	require.NoError(t, err)
	require.NoError(t, backend.PutTerraformState(project.ID, inventory.ID, ciphertext, ""))

	state, err := backend.GetLatestTerraformState(project.ID, inventory.ID)
	require.NoError(t, err)
	assert.NotEqual(t, `{"version":4}`, state.State)
	plain, err := util.Config.DecryptAccessSecret(state.State)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":4}`, string(plain))

	_, err = backend.AcquireTerraformStateLock(project.ID, inventory.ID, db.TerraformStateLock{ID: "owner", Info: `{"ID":"owner"}`})
	require.NoError(t, err)
	held, err := secondBackend.AcquireTerraformStateLock(project.ID, inventory.ID, db.TerraformStateLock{ID: "other", Info: `{"ID":"other"}`})
	assert.ErrorIs(t, err, ErrTerraformStateLocked)
	assert.Equal(t, "owner", held.ID)
	assert.ErrorIs(t, backend.PutTerraformState(project.ID, inventory.ID, ciphertext, "other"), ErrTerraformStateLocked)
	assert.ErrorIs(t, secondBackend.ReleaseTerraformStateLock(project.ID, inventory.ID, "other"), ErrTerraformStateLocked)
	assert.ErrorIs(t, secondBackend.DeleteLatestTerraformState(project.ID, inventory.ID, "other"), ErrTerraformStateLocked)
	assert.ErrorIs(t, secondBackend.DeleteTerraformStateIfCurrent(project.ID, inventory.ID, state.ID, ""), ErrTerraformStateLocked)
	require.NoError(t, backend.PutTerraformState(project.ID, inventory.ID, ciphertext, "owner"))
	require.NoError(t, backend.DeleteLatestTerraformState(project.ID, inventory.ID, "owner"))
	_, err = backend.GetLatestTerraformState(project.ID, inventory.ID)
	assert.ErrorIs(t, err, db.ErrNotFound)
	require.NoError(t, backend.ReleaseTerraformStateLock(project.ID, inventory.ID, "owner"))

	legacyInventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Name: "legacy", Type: db.InventoryTerraformWorkspace})
	require.NoError(t, err)
	require.NoError(t, backend.DeleteLatestTerraformState(project.ID, legacyInventory.ID, ""), "empty HTTP backend deletes are idempotent")
	_, err = store.GetConnection().Exec("insert into project__terraform_inventory_state(project_id, inventory_id, state, created) values (?, ?, ?, CURRENT_TIMESTAMP)", project.ID, legacyInventory.ID, `{"version":4,"legacy":true}`)
	require.NoError(t, err)
	legacy, err := backend.GetLatestTerraformState(project.ID, legacyInventory.ID)
	require.NoError(t, err)
	assert.Empty(t, util.SecretKeyID(legacy.State), "legacy plaintext requires vault rekey before the HTTP backend can serve it")
	require.NoError(t, backend.RekeyTerraformStates(""))
	legacy, err = backend.GetLatestTerraformState(project.ID, legacyInventory.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, util.SecretKeyID(legacy.State))
	plain, err = util.Config.DecryptAccessSecret(legacy.State)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":4,"legacy":true}`, string(plain))
}

func TestTerraformStateManagementDeleteCannotTombstoneNewerState(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString(make([]byte, 32))
	project, err := store.CreateProject(db.Project{Name: "Terraform conditional state deletion"})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{ProjectID: project.ID, Name: "state", Type: db.InventoryTerraformWorkspace})
	require.NoError(t, err)
	backend := NewTerraformStore(store.GetConnection())
	first, err := util.Config.EncryptAccessSecret([]byte(`{"version":4,"serial":1}`))
	require.NoError(t, err)
	require.NoError(t, backend.PutTerraformState(project.ID, inventory.ID, first, ""))
	observed, err := backend.GetLatestTerraformState(project.ID, inventory.ID)
	require.NoError(t, err)
	second, err := util.Config.EncryptAccessSecret([]byte(`{"version":4,"serial":2}`))
	require.NoError(t, err)
	require.NoError(t, backend.PutTerraformState(project.ID, inventory.ID, second, ""))

	assert.ErrorIs(t, backend.DeleteTerraformStateIfCurrent(project.ID, inventory.ID, observed.ID, ""), ErrTerraformStateLocked)
	current, err := backend.GetLatestTerraformState(project.ID, inventory.ID)
	require.NoError(t, err)
	plain, err := util.Config.DecryptAccessSecret(current.State)
	require.NoError(t, err)
	assert.JSONEq(t, `{"version":4,"serial":2}`, string(plain))
}
