package db

// NormalizeSharedCheckout removes a Git checkout policy from filesystem paths.
// Apply it to input copies so legacy records and backups remain usable without
// rewriting stored history merely to read or execute them.
func (r *Repository) NormalizeSharedCheckout() {
	if r.GetType() == RepositoryLocal {
		r.Monorepo = false
	}
}
