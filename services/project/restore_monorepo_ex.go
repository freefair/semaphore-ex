package project

import "fmt"

// restoreMonorepoPolicies retains legacy template settings which became
// inactive when an operator enabled shared checkout after creating templates.
func (backup *BackupFormat) restoreMonorepoPolicies(b *BackupDB) error {
	for i, source := range backup.Repositories {
		if !source.Monorepo {
			continue
		}
		repository := b.repositories[i]
		repository.Monorepo = true
		if err := b.store.UpdateRepository(repository); err != nil {
			return fmt.Errorf("error restoring monorepo policy for repositories[%d]: %w", i, err)
		}
		b.repositories[i] = repository
	}
	return nil
}
