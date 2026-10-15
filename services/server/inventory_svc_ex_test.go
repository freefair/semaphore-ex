package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInventoryServiceDropsStaleRepositoryFromStaticInventory(t *testing.T) {
	for _, inventoryType := range []db.InventoryType{
		db.InventoryStatic, db.InventoryStaticYaml, db.InventoryTerraformWorkspace,
		db.InventoryTofuWorkspace, db.InventoryTerragruntWorkspace,
	} {
		t.Run(string(inventoryType), func(t *testing.T) {
			testInventoryServiceDropsStaleRepository(t, inventoryType)
		})
	}
}

func testInventoryServiceDropsStaleRepository(t *testing.T, inventoryType db.InventoryType) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "inventory service stale repository"})
	require.NoError(t, err)
	otherProject, err := store.CreateProject(db.Project{Name: "foreign inventory repository"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &otherProject.ID, Name: "repository key", Type: db.AccessKeyNone})
	require.NoError(t, err)
	foreignRepository, err := store.CreateRepository(db.Repository{
		ProjectID: otherProject.ID, SSHKeyID: key.ID, Name: "foreign repository",
		GitURL: "https://example.invalid/foreign.git", GitBranch: "main",
	})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{
		ProjectID: project.ID, Name: "static", Type: inventoryType, Inventory: "localhost",
	})
	require.NoError(t, err)
	_, err = store.Sql().Exec(store.PrepareQuery("update project__inventory set repository_id=? where id=?"), foreignRepository.ID, inventory.ID)
	require.NoError(t, err)

	loaded, err := NewInventoryService(store, store, store, NewAccessKeyEncryptionService(store, store, store, store)).GetInventory(project.ID, inventory.ID)
	require.NoError(t, err)
	assert.Nil(t, loaded.RepositoryID)
	assert.Nil(t, loaded.Repository)
}

func TestInventoryServiceKeepsRepositoryForFileInventory(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "inventory service file repository"})
	require.NoError(t, err)
	key, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "repository key", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: key.ID, Name: "inventory repository",
		GitURL: "https://example.invalid/inventory.git", GitBranch: "main",
	})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{
		ProjectID: project.ID, Name: "file", Type: db.InventoryFile, Inventory: "hosts.ini",
		RepositoryID: &repository.ID,
	})
	require.NoError(t, err)

	loaded, err := NewInventoryService(store, store, store, NewAccessKeyEncryptionService(store, store, store, store)).GetInventory(project.ID, inventory.ID)
	require.NoError(t, err)
	require.NotNil(t, loaded.RepositoryID)
	assert.Equal(t, repository.ID, *loaded.RepositoryID)
	require.NotNil(t, loaded.Repository)
	assert.Equal(t, repository.ID, loaded.Repository.ID)
}
