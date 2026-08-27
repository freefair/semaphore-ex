package sql

import (
	"errors"
	"github.com/semaphoreui/semaphore/db"
	"time"
)

func (d *SqlDb) GetSyncEnabledSecretSyncs() (syncs []db.SecretSync, err error) {
	syncs = make([]db.SecretSync, 0)
	_, err = d.selectAll(
		&syncs,
		"select * from project__secret_sync where sync_enabled=? and sync_interval>0",
		true,
	)
	if err != nil {
		return
	}
	for i := range syncs {
		syncs[i].Paths, err = d.getSecretSyncPaths(syncs[i].ID)
		if err != nil {
			return
		}
	}
	return
}

func (d *SqlDb) MarkSecretSyncSynced(syncID int, success bool, at time.Time) error {
	var query string
	if success {
		query = "update project__secret_sync set last_synced_at=? where id=?"
	} else {
		query = "update project__secret_sync set last_sync_failed_at=? where id=?"
	}
	_, err := d.exec(query, at, syncID)
	return err
}

func (d *SqlDb) GetStorageSecretSync(storageID int) (sync db.SecretSync, err error) {
	return d.getSecretSyncByOwner(storageID, nil)
}

func (d *SqlDb) GetEnvironmentSecretSync(environmentID int) (sync db.SecretSync, err error) {
	return d.getSecretSyncByOwner(0, &environmentID)
}

func (d *SqlDb) getSecretSyncByOwner(storageID int, environmentID *int) (sync db.SecretSync, err error) {
	var query string
	var args []any
	if environmentID == nil {
		query = "select * from project__secret_sync where storage_id=? and environment_id is null"
		args = []any{storageID}
	} else {
		query = "select * from project__secret_sync where environment_id=?"
		args = []any{*environmentID}
	}

	err = d.selectOne(&sync, query, args...)
	if err != nil {
		return
	}

	sync.Paths, err = d.getSecretSyncPaths(sync.ID)
	if sync.Direction == "" {
		sync.Direction = db.SecretSyncDirectionReadOnly
	}
	return
}

func (d *SqlDb) SaveSecretSync(sync db.SecretSync) error {
	if sync.Direction == "" {
		sync.Direction = db.SecretSyncDirectionReadOnly
	}
	if err := sync.Direction.Validate(); err != nil {
		return err
	}
	// If the row can't carry any info (disabled with no paths), remove it
	// entirely instead of keeping a blank row around.
	if sync.Direction == db.SecretSyncDirectionReadOnly && !sync.SyncEnabled &&
		sync.SyncInterval == 0 && len(sync.Paths) == 0 {
		return d.deleteSecretSync(sync.StorageID, sync.EnvironmentID)
	}

	existing, err := d.getSecretSyncByOwner(sync.StorageID, sync.EnvironmentID)
	if sync.Direction == db.SecretSyncDirectionOutbound {
		seen := make(map[string]struct{}, len(sync.Paths))
		for _, path := range sync.Paths {
			if err := path.ValidateManaged(); err != nil {
				return err
			}
			identity := path.Mount + "\x00" + path.Path + "\x00" + path.Field
			if _, exists := seen[identity]; exists {
				return errors.New("managed secret target is duplicated")
			}
			seen[identity] = struct{}{}
		}
	}

	var syncID int
	switch err {
	case nil:
		syncID = existing.ID
		sync.Revision = existing.Revision + 1
		if _, err = d.exec(
			"update project__secret_sync set sync_enabled=?, sync_interval=?, direction=?, revision=? where id=?",
			sync.SyncEnabled, sync.SyncInterval, sync.Direction, sync.Revision, syncID,
		); err != nil {
			return err
		}
	case db.ErrNotFound:
		sync.Revision = 1
		syncID, err = d.insert(
			"id",
			"insert into project__secret_sync "+
				"(project_id, storage_id, environment_id, sync_enabled, sync_interval, direction, revision) "+
				"values (?, ?, ?, ?, ?, ?, ?)",
			sync.ProjectID, sync.StorageID, sync.EnvironmentID,
			sync.SyncEnabled, sync.SyncInterval, sync.Direction, sync.Revision,
		)
		if err != nil {
			return err
		}
	default:
		return err
	}

	return d.saveSecretSyncPaths(syncID, sync.Direction, sync.Paths)
}

func (d *SqlDb) deleteSecretSync(storageID int, environmentID *int) error {
	var query string
	var args []any
	if environmentID == nil {
		query = "delete from project__secret_sync where storage_id=? and environment_id is null"
		args = []any{storageID}
	} else {
		query = "delete from project__secret_sync where environment_id=?"
		args = []any{*environmentID}
	}
	_, err := d.exec(query, args...)
	return err
}

func (d *SqlDb) getSecretSyncPaths(syncID int) (paths []db.SecretSyncPath, err error) {
	paths = make([]db.SecretSyncPath, 0)
	_, err = d.selectAll(
		&paths,
		"select id, sync_id, path, prefix, `separator`, access_key_id, mount, field, "+
			"remote_version, content_fingerprint "+
			"from project__secret_sync_path where sync_id=? order by id",
		syncID,
	)
	return
}

func (d *SqlDb) replaceSecretSyncPaths(syncID int, paths []db.SecretSyncPath) error {
	if _, err := d.exec("delete from project__secret_sync_path where sync_id=?", syncID); err != nil {
		return err
	}
	for _, p := range paths {
		if _, err := d.insert(
			"id",
			"insert into project__secret_sync_path "+
				"(sync_id, path, prefix, `separator`, access_key_id, mount, field, remote_version, content_fingerprint) "+
				"values (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			syncID, p.Path, p.Prefix, p.Separator, nil, "", "", 0, "",
		); err != nil {
			return err
		}
	}
	return nil
}
