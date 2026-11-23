package tasks

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sshcrypto "golang.org/x/crypto/ssh"
)

const inventoryBecomePassword = "become-secret"

// newInventoryKeyTask stores the inventory user and become keys encrypted, as
// the API does, and returns a task whose inventory uses them. With snapshot
// set, the task carries an execution snapshot of that inventory.
func newInventoryKeyTask(t *testing.T, snapshot bool) (TaskPool, db.Task, db.Inventory, string) {
	t.Helper()
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	tmp, err := os.MkdirTemp(taskSSHControlPathParent(), "sxk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	util.Config.TmpPath = tmp
	util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	encryption := server.NewAccessKeyEncryptionService(store, store, store, store)

	project, err := store.CreateProject(db.Project{Name: "inventory keys"})
	require.NoError(t, err)
	_, ed25519Key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := sshcrypto.MarshalPrivateKey(ed25519Key, "")
	require.NoError(t, err)
	privateKey := string(pem.EncodeToMemory(block))

	createEncrypted := func(key db.AccessKey) db.AccessKey {
		key.ProjectID = &project.ID
		require.NoError(t, encryption.SerializeSecret(&key))
		created, createErr := store.CreateAccessKey(key)
		require.NoError(t, createErr)
		stored, getErr := store.GetAccessKey(project.ID, created.ID)
		require.NoError(t, getErr)
		require.NotNil(t, stored.Secret)
		require.Empty(t, stored.SshKey.PrivateKey, "the store must return keys still encrypted")
		require.Empty(t, stored.LoginPassword.Password, "the store must return keys still encrypted")
		return created
	}
	userKey := createEncrypted(db.AccessKey{
		Name: "inventory user", Type: db.AccessKeySSH,
		SshKey: db.SshKey{Login: "deploy", PrivateKey: privateKey},
	})
	becomeKey := createEncrypted(db.AccessKey{
		Name: "inventory become", Type: db.AccessKeyLoginPassword,
		LoginPassword: db.LoginPassword{Login: "root", Password: inventoryBecomePassword},
	})

	repositoryKey, err := store.CreateAccessKey(db.AccessKey{ProjectID: &project.ID, Name: "repository", Type: db.AccessKeyNone})
	require.NoError(t, err)
	repository, err := store.CreateRepository(db.Repository{
		ProjectID: project.ID, SSHKeyID: repositoryKey.ID, Name: "repository",
		GitURL: "https://example.invalid/playbooks.git", GitBranch: "main",
	})
	require.NoError(t, err)
	inventory, err := store.CreateInventory(db.Inventory{
		ProjectID: project.ID, Name: "hosts", Type: db.InventoryStatic,
		Inventory: "host.example.invalid", SSHKeyID: &userKey.ID, BecomeKeyID: &becomeKey.ID,
	})
	require.NoError(t, err)
	template, err := store.CreateTemplate(db.Template{
		ProjectID: project.ID, RepositoryID: repository.ID, InventoryID: &inventory.ID,
		Name: "upgrade", App: db.AppAnsible, Playbook: "upgrade.yml",
	})
	require.NoError(t, err)

	task := db.Task{ProjectID: project.ID, TemplateID: template.ID}
	if snapshot {
		snapshotInventory := sanitizedExecutionSnapshotInventory(inventory)
		encoded, encodeErr := db.EncodeTaskExecutionSnapshot(db.TaskExecutionSnapshot{
			Version: db.TaskExecutionSnapshotVersion, ProjectID: project.ID, Fingerprint: "inventory-keys",
			Template:   sanitizedExecutionSnapshotTemplate(template),
			Repository: sanitizedExecutionSnapshotRepository(repository),
			Inventory:  &snapshotInventory,
		})
		require.NoError(t, encodeErr)
		task.ExecutionSnapshotJSON = &encoded
	}
	task, err = store.CreateTask(task, 0)
	require.NoError(t, err)

	pool := CreateTaskPool(store, NewMemoryTaskStateStore(), nil,
		server.NewInventoryService(store, store, store, encryption),
		encryption, &KeyInstallerMock{}, &mockLogWriteService{}, nil, nil)
	return pool, task, inventory, privateKey
}

func TestPopulateDetailsDecryptsInventoryKeys(t *testing.T) {
	for _, path := range []struct {
		name     string
		snapshot bool
	}{{"live", false}, {"snapshot", true}} {
		t.Run(path.name+" decrypts keys and starts the SSH agent", func(t *testing.T) {
			pool, task, _, privateKey := newInventoryKeyTask(t, path.snapshot)
			runner := NewTaskRunner(task, &pool, "", nil)

			require.NoError(t, runner.populateDetails())

			assert.Equal(t, "deploy", runner.Inventory.SSHKey.SshKey.Login)
			assert.Equal(t, privateKey, runner.Inventory.SSHKey.SshKey.PrivateKey)
			assert.Equal(t, "root", runner.Inventory.BecomeKey.LoginPassword.Login)
			assert.Equal(t, inventoryBecomePassword, runner.Inventory.BecomeKey.LoginPassword.Password)
			executor := &LocalExecutor{
				Task: runner.Task, Template: runner.Template,
				Inventory: runner.Inventory, Repository: runner.Repository, Logger: runner,
			}
			t.Cleanup(executor.Cleanup)
			require.NoError(t, executor.startTaskSSHAgent())
			assert.NotNil(t, executor.taskSSHAgent)
		})

		t.Run(path.name+" reports which key failed to decrypt", func(t *testing.T) {
			pool, task, inventory, _ := newInventoryKeyTask(t, path.snapshot)
			// A different configured key cannot decrypt the stored secrets.
			util.Config.AccessKeyEncryption = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
			runner := NewTaskRunner(task, &pool, "", nil)

			err := runner.populateDetails()

			require.ErrorContains(t, err, fmt.Sprintf("resolving inventory SSH key %d: ", *inventory.SSHKeyID))
			assert.Empty(t, runner.Inventory.SSHKey.SshKey.PrivateKey)
			assert.Empty(t, runner.Inventory.BecomeKey.LoginPassword.Password)
		})
	}
}
