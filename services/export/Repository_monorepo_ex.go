package export

import "github.com/semaphoreui/semaphore/db"

// postRestore reuses the importer's deferred-reference phase so that legacy
// template settings survive without changing the shared repository policy.
func (e *RepositoryExporter) postRestore(store db.Store, _ DataExporter) error {
	for _, repository := range e.pendingMonorepos {
		repository.Monorepo = true
		if err := store.UpdateRepository(repository); err != nil {
			return err
		}
	}
	e.pendingMonorepos = nil
	return nil
}
