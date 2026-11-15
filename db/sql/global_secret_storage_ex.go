package sql

import (
	"sort"
	"strings"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
)

var _ db.GlobalSecretStorageRepository = (*SqlDb)(nil)

func (d *SqlDb) GetGlobalSecretStorages() ([]db.GlobalSecretStorage, error) {
	storages := make([]db.GlobalSecretStorage, 0)
	if _, err := d.selectAll(&storages, "select * from global_secret_storage order by name"); err != nil {
		return nil, err
	}
	for i := range storages {
		if err := d.fillGlobalSecretStorageProjects(&storages[i]); err != nil {
			return nil, err
		}
	}
	return storages, nil
}

func (d *SqlDb) GetGlobalSecretStorage(storageID int) (db.GlobalSecretStorage, error) {
	var storage db.GlobalSecretStorage
	if err := d.selectOne(&storage, "select * from global_secret_storage where id=?", storageID); err != nil {
		return db.GlobalSecretStorage{}, err
	}
	err := d.fillGlobalSecretStorageProjects(&storage)
	return storage, err
}

// GetGlobalSecretStoragesForProject returns the storages granted to a
// project, ordered by ID so a task's PAM configuration is deterministic.
func (d *SqlDb) GetGlobalSecretStoragesForProject(projectID int) ([]db.GlobalSecretStorage, error) {
	storages := make([]db.GlobalSecretStorage, 0)
	if _, err := d.selectAll(&storages,
		"select s.* from global_secret_storage s join global_secret_storage_project p on p.storage_id = s.id "+
			"where p.project_id=? order by s.id", projectID); err != nil {
		return nil, err
	}
	for i := range storages {
		if err := d.fillGlobalSecretStorageProjects(&storages[i]); err != nil {
			return nil, err
		}
	}
	return storages, nil
}

func (d *SqlDb) CreateGlobalSecretStorage(storage db.GlobalSecretStorage) (db.GlobalSecretStorage, error) {
	storage.Name = strings.TrimSpace(storage.Name)
	if storage.Name == "" || len(storage.Name) > 128 || storage.Type == "" {
		return db.GlobalSecretStorage{}, db.ErrInvalidOperation
	}
	if storage.Params == nil {
		storage.Params = db.MapStringAnyField{}
	}
	now := tz.Now()
	params, err := storage.Params.Value()
	if err != nil {
		return db.GlobalSecretStorage{}, err
	}
	insertID, err := d.insert("id",
		"insert into global_secret_storage (name, type, params, credential_key_id, revision, created, updated) values (?, ?, ?, ?, 1, ?, ?)",
		storage.Name, string(storage.Type), params, storage.CredentialKeyID, now, now)
	if err != nil {
		return db.GlobalSecretStorage{}, err
	}
	// The row and its project grants become visible together: a failed grant
	// write removes the row again instead of leaving a storage without them.
	tx, err := d.Sql().Begin()
	if err == nil {
		if err = d.replaceGlobalSecretStorageProjectsTx(tx, insertID, storage.ProjectIDs); err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}
	if err != nil {
		_, _ = d.exec("delete from global_secret_storage where id=?", insertID)
		return db.GlobalSecretStorage{}, err
	}
	return d.GetGlobalSecretStorage(insertID)
}

func (d *SqlDb) UpdateGlobalSecretStorage(storage db.GlobalSecretStorage, expectedRevision int) (db.GlobalSecretStorage, error) {
	storage.Name = strings.TrimSpace(storage.Name)
	if storage.ID <= 0 || expectedRevision <= 0 || storage.Name == "" || len(storage.Name) > 128 {
		return db.GlobalSecretStorage{}, db.ErrInvalidOperation
	}
	if storage.Params == nil {
		storage.Params = db.MapStringAnyField{}
	}
	params, err := storage.Params.Value()
	if err != nil {
		return db.GlobalSecretStorage{}, err
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.GlobalSecretStorage{}, err
	}
	result, err := tx.Exec(d.PrepareQuery(
		"update global_secret_storage set name=?, params=?, credential_key_id=?, revision=revision+1, updated=? where id=? and revision=?"),
		storage.Name, params, storage.CredentialKeyID, tz.Now(), storage.ID, expectedRevision)
	if err != nil {
		_ = tx.Rollback()
		return db.GlobalSecretStorage{}, err
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialRevisionConflict); err != nil {
		_ = tx.Rollback()
		return db.GlobalSecretStorage{}, err
	}
	if err = d.replaceGlobalSecretStorageProjectsTx(tx, storage.ID, storage.ProjectIDs); err != nil {
		_ = tx.Rollback()
		return db.GlobalSecretStorage{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.GlobalSecretStorage{}, err
	}
	return d.GetGlobalSecretStorage(storage.ID)
}

func (d *SqlDb) DeleteGlobalSecretStorage(storageID int) error {
	result, err := d.exec("delete from global_secret_storage where id=?", storageID)
	if err != nil {
		return err
	}
	return requireOneRow(result, db.ErrNotFound)
}

// GetGlobalSecretStorageKey loads only a project-less key owned by a global
// storage, so a project key ID can never be used through this path.
func (d *SqlDb) GetGlobalSecretStorageKey(keyID int) (db.AccessKey, error) {
	var key db.AccessKey
	err := d.selectOne(&key,
		"select * from access"+"_key where id=? and project_id is null and owner=?",
		keyID, string(db.AccessKeyGlobalSecretStorage))
	return key, err
}

func (d *SqlDb) DeleteGlobalSecretStorageKey(keyID int) error {
	_, err := d.exec("delete from access"+"_key where id=? and project_id is null and owner=?",
		keyID, string(db.AccessKeyGlobalSecretStorage))
	return err
}

func (d *SqlDb) fillGlobalSecretStorageProjects(storage *db.GlobalSecretStorage) error {
	var rows []struct {
		ProjectID int `db:"project_id"`
	}
	if _, err := d.selectAll(&rows, "select project_id from global_secret_storage_project where storage_id=? order by project_id", storage.ID); err != nil {
		return err
	}
	storage.ProjectIDs = make([]int, 0, len(rows))
	for _, row := range rows {
		storage.ProjectIDs = append(storage.ProjectIDs, row.ProjectID)
	}
	return nil
}

func (d *SqlDb) replaceGlobalSecretStorageProjectsTx(tx gorp.SqlExecutor, storageID int, projectIDs []int) error {
	if _, err := tx.Exec(d.PrepareQuery("delete from global_secret_storage_project where storage_id=?"), storageID); err != nil {
		return err
	}
	unique := make(map[int]struct{}, len(projectIDs))
	ordered := make([]int, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		if projectID <= 0 {
			return db.ErrInvalidOperation
		}
		if _, seen := unique[projectID]; !seen {
			unique[projectID] = struct{}{}
			ordered = append(ordered, projectID)
		}
	}
	sort.Ints(ordered)
	for _, projectID := range ordered {
		if _, err := tx.Exec(d.PrepareQuery("insert into global_secret_storage_project (storage_id, project_id) values (?, ?)"), storageID, projectID); err != nil {
			return err
		}
	}
	return nil
}
