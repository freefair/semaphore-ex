package tasks

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/server"
)

// refreshInfisicalPAM loads the project's Infisical PAM routes and decrypts
// their machine identity secrets only after this node owns the task start,
// like host mapping credentials. The result lives on the in-memory task and
// never persists.
func (t *TaskRunner) refreshInfisicalPAM() error {
	configs, err := server.LoadInfisicalPAMForProject(t.pool.store, t.pool.store, globalSecretStorages(t.pool.store), t.Template.ProjectID)
	if err != nil {
		return err
	}
	for index := range configs {
		if err = t.pool.encryptionService.DeserializeSecret(&configs[index].Credential); err != nil {
			return err
		}
	}
	t.Task.InfisicalPAM = configs
	if localJob := t.localJob(); localJob != nil {
		localJob.Task.InfisicalPAM = configs
	}
	return nil
}

// RefreshInfisicalPAMForDispatch reloads the routes and secrets on the node
// that dispatches the task to a remote runner.
func (t *TaskRunner) RefreshInfisicalPAMForDispatch() error {
	return t.refreshInfisicalPAM()
}

// globalSecretStorages returns the store's global storages, or nil when the
// store does not persist them; their routes then do not apply.
func globalSecretStorages(store db.Store) db.GlobalSecretStorageRepository {
	if repo, ok := store.(db.GlobalSecretStorageRepository); ok {
		return repo
	}
	return nil
}
