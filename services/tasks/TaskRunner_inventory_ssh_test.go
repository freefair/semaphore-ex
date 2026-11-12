package tasks

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sshcrypto "golang.org/x/crypto/ssh"
)

type inventorySSHEncryptionService struct {
	EncryptionServiceMock
	keyID    int
	material string
	fail     bool
}

func (s *inventorySSHEncryptionService) DeserializeSecret(key *db.AccessKey) error {
	if key.ID != s.keyID {
		return nil
	}
	if s.fail {
		return errors.New("credential unavailable")
	}
	key.SshKey.PrivateKey = s.material
	return nil
}

func TestPopulateDetailsResolvesInventorySSHKeyBeforeAgent(t *testing.T) {
	for _, test := range []struct {
		name     string
		fail     bool
		snapshot bool
	}{
		{name: "live valid key"},
		{name: "live resolution failure", fail: true},
		{name: "snapshot valid key", snapshot: true},
		{name: "snapshot resolution failure", snapshot: true, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newTaskRunnerRunFixture(t)
			t.Cleanup(fixture.store.Close)
			key, err := fixture.store.CreateAccessKey(db.AccessKey{
				ProjectID: &fixture.task.ProjectID, Name: "inventory", Type: db.AccessKeySSH,
			})
			require.NoError(t, err)
			inventory, err := fixture.store.GetInventory(fixture.task.ProjectID, *fixture.template.InventoryID)
			require.NoError(t, err)
			inventory.SSHKeyID = &key.ID
			require.NoError(t, fixture.store.UpdateInventory(inventory))
			_, privateKey, err := ed25519.GenerateKey(rand.Reader)
			require.NoError(t, err)
			block, err := sshcrypto.MarshalPrivateKey(privateKey, "")
			require.NoError(t, err)
			encryption := &inventorySSHEncryptionService{
				keyID: key.ID, material: string(pem.EncodeToMemory(block)), fail: test.fail,
			}
			fixture.pool.encryptionService = encryption
			fixture.pool.inventoryService = server.NewInventoryService(
				fixture.store, fixture.store, fixture.store, encryption,
			)
			runner := &TaskRunner{Task: fixture.task, pool: &fixture.pool}
			if test.snapshot {
				repository, err := fixture.store.GetRepository(fixture.task.ProjectID, fixture.template.RepositoryID)
				require.NoError(t, err)
				encoded, err := db.EncodeTaskExecutionSnapshot(db.TaskExecutionSnapshot{
					Version: db.TaskExecutionSnapshotVersion, ProjectID: fixture.task.ProjectID,
					Fingerprint: "inventory-ssh-regression",
					Template:    sanitizedExecutionSnapshotTemplate(fixture.template),
					Repository:  sanitizedExecutionSnapshotRepository(repository),
					Inventory:   &inventory,
				})
				require.NoError(t, err)
				runner.Task.ExecutionSnapshotJSON = &encoded
			}
			err = runner.populateDetails()
			if test.fail {
				require.ErrorContains(t, err, "resolving inventory SSH key")
				assert.Empty(t, runner.Inventory.SSHKey.SshKey.PrivateKey)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, encryption.material, runner.Inventory.SSHKey.SshKey.PrivateKey)
			executor := &LocalExecutor{
				Task: runner.Task, Template: runner.Template,
				Inventory: runner.Inventory, Repository: runner.Repository, Logger: runner,
			}
			require.NoError(t, executor.startTaskSSHAgent())
			t.Cleanup(func() { executor.Cleanup() })
			require.NotNil(t, executor.taskSSHAgent)
		})
	}
}
