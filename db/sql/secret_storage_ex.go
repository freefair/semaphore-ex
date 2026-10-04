package sql

import "github.com/semaphoreui/semaphore/db"

func (d *SqlDb) deleteSecretStorageAtomic(projectID int, storageID int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// MySQL accepts inline REFERENCES clauses as syntax but does not create the
	// constraint. Check the tenant-scoped parent before its references so a
	// cross-project ID still returns ErrNotFound.
	exists, err := tx.SelectInt(d.PrepareQuery("select count(*) from project__secret_storage where project_id=? and id=?"), projectID, storageID)
	if err != nil {
		return err
	}
	if exists == 0 {
		return db.ErrNotFound
	}

	for _, query := range []string{
		"select count(*) from project where default_secret_storage_id=?",
		"select count(*) from project__environment where secret_storage_id=?",
	} {
		refs, err := tx.SelectInt(d.PrepareQuery(query), storageID)
		if err != nil {
			return err
		}
		if refs > 0 {
			return db.ErrInvalidOperation
		}
	}

	syncEnabled, err := tx.SelectInt(d.PrepareQuery("select count(*) from project__secret_sync where storage_id=? and environment_id is null and sync_enabled=?"), storageID, true)
	if err != nil {
		return err
	}

	// Owned credentials are deleted only when their vault owner matches. Source
	// credentials are deleted only for enabled sync, preserving the service
	// contract that existed before this became a single atomic operation.
	credentialFilter := "(project_id=? and owner=? and storage_id=?)"
	credentialArgs := []any{projectID, db.AccessKeySecretStorage, storageID}
	if syncEnabled > 0 {
		credentialFilter += " or (project_id=? and source_storage_id=?)"
		credentialArgs = append(credentialArgs, projectID, storageID)
	}

	outsideSourceQuery := "select count(*) from access_key where source_storage_id=?"
	outsideSourceArgs := []any{storageID}
	if syncEnabled > 0 {
		outsideSourceQuery += " and (project_id<>? or project_id is null)"
		outsideSourceArgs = append(outsideSourceArgs, projectID)
	}
	for _, query := range []struct {
		query string
		args  []any
	}{
		{"select count(*) from access_key where storage_id=? and (project_id<>? or project_id is null or owner<>?)", []any{storageID, projectID, db.AccessKeySecretStorage}},
		{outsideSourceQuery, outsideSourceArgs},
	} {
		refs, err := tx.SelectInt(d.PrepareQuery(query.query), query.args...)
		if err != nil {
			return err
		}
		if refs > 0 {
			return db.ErrInvalidOperation
		}
	}

	// Inventory key columns were also added with inline REFERENCES. Check their
	// active credential bindings in this transaction, before deleting a key.
	inventoryArgs := append([]any{projectID}, credentialArgs...)
	inventoryArgs = append(inventoryArgs, credentialArgs...)
	inventoryRefs, err := tx.SelectInt(d.PrepareQuery(
		"select count(*) from project__inventory i where i.project_id=? and ("+
			"i.ssh_key_id in (select id from access_key where "+credentialFilter+") or "+
			"i.become_key_id in (select id from access_key where "+credentialFilter+"))"), inventoryArgs...)
	if err != nil {
		return err
	}
	if inventoryRefs > 0 {
		return db.ErrInvalidOperation
	}

	if _, err = tx.Exec(d.PrepareQuery("delete from access_key where "+credentialFilter), credentialArgs...); err != nil {
		return validateMutationResult(nil, err)
	}
	result, err := tx.Exec(d.PrepareQuery("delete from project__secret_storage where project_id=? and id=?"), projectID, storageID)
	if err = requireDeletedRow(result, err); err != nil {
		return err
	}
	return tx.Commit()
}
