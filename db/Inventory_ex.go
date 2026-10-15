package db

// UsesRepository reports whether this inventory needs its own repository at
// execution time. Old clients can leave a repository_id behind after changing
// a file inventory to another type; that stale reference is not executable.
func (e Inventory) UsesRepository() bool {
	return e.Type == InventoryFile && e.RepositoryID != nil
}

// NormalizeRepositoryReference removes a repository reference that cannot
// affect execution. Callers operate on an execution copy, never the persisted
// inventory row, so legacy records remain readable while their unused
// credential is kept out of the execution path.
func (e *Inventory) NormalizeRepositoryReference() {
	if !e.UsesRepository() {
		e.RepositoryID = nil
		e.Repository = nil
	}
}
