package tasks

import (
	"errors"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type decryptingEncryptionService struct {
	EncryptionServiceMock
	fail bool
}

func (s *decryptingEncryptionService) DeserializeSecret(key *db.AccessKey) error {
	if s.fail {
		return errors.New("cannot decrypt")
	}
	key.String = "decrypted-secret"
	return nil
}

func createInfisicalPAMStorage(t *testing.T, store *sql.SqlDb, projectID int, routes []any) db.SecretStorage {
	t.Helper()
	storage, err := store.CreateSecretStorage(db.SecretStorage{
		ProjectID: projectID, Name: "pam", Type: db.SecretStorageTypeInfisical, ReadOnly: true,
		Params: db.MapStringAnyField{
			"url": "https://infisical.example.com", "client_id": "client-1",
			"session_duration": "30m0s", "pam_routes": routes,
		},
	})
	require.NoError(t, err)
	_, err = store.CreateAccessKey(db.AccessKey{
		Name: "pam secret", Type: db.AccessKeyString, ProjectID: &projectID,
		Owner: db.AccessKeySecretStorage, StorageID: &storage.ID,
	})
	require.NoError(t, err)
	return storage
}

func TestRefreshInfisicalPAMHydratesTaskAndLocalJob(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "pam"})
	require.NoError(t, err)
	storage := createInfisicalPAMStorage(t, store, project.ID, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}})
	runner := &TaskRunner{
		pool:     &TaskPool{store: store, encryptionService: &decryptingEncryptionService{}},
		Template: db.Template{ProjectID: project.ID},
	}
	job := &LocalExecutor{}
	runner.job = job

	require.NoError(t, runner.refreshInfisicalPAM())

	require.Len(t, runner.Task.InfisicalPAM, 1)
	config := runner.Task.InfisicalPAM[0]
	assert.Equal(t, storage.ID, config.StorageID)
	assert.Equal(t, "30m0s", config.SessionDuration)
	assert.Equal(t, []db.InfisicalPAMRoute{{Host: "web-1", Account: "servers/web-1"}}, config.Routes)
	assert.Equal(t, "decrypted-secret", config.Credential.String)
	assert.Equal(t, config.CredentialKeyID, config.Credential.ID)
	assert.Equal(t, runner.Task.InfisicalPAM, job.Task.InfisicalPAM)
}

func TestRefreshInfisicalPAMFailsWhenSecretCannotBeDecrypted(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "pam"})
	require.NoError(t, err)
	createInfisicalPAMStorage(t, store, project.ID, []any{map[string]any{"host": "web-1", "account": "servers/web-1"}})
	runner := &TaskRunner{
		pool:     &TaskPool{store: store, encryptionService: &decryptingEncryptionService{fail: true}},
		Template: db.Template{ProjectID: project.ID},
	}

	assert.Error(t, runner.refreshInfisicalPAM())
	assert.Empty(t, runner.Task.InfisicalPAM)
}

func TestRefreshInfisicalPAMIgnoresProjectsWithoutRoutes(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "pam"})
	require.NoError(t, err)
	createInfisicalPAMStorage(t, store, project.ID, []any{})
	runner := &TaskRunner{
		pool:     &TaskPool{store: store, encryptionService: &decryptingEncryptionService{fail: true}},
		Template: db.Template{ProjectID: project.ID},
	}

	require.NoError(t, runner.refreshInfisicalPAM())
	assert.Empty(t, runner.Task.InfisicalPAM)
}
