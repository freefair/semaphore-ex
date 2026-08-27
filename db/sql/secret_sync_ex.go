package sql

import (
	"github.com/semaphoreui/semaphore/db"
)

func (d *SqlDb) GetSecretSync(syncID int) (sync db.SecretSync, err error) {
	err = d.selectOne(&sync, "select * from project__secret_sync where id=?", syncID)
	if err != nil {
		return
	}
	if sync.Direction == "" {
		sync.Direction = db.SecretSyncDirectionReadOnly
	}
	sync.Paths, err = d.getSecretSyncPaths(sync.ID)
	return
}

func (d *SqlDb) saveSecretSyncPaths(
	syncID int,
	direction db.SecretSyncDirection,
	paths []db.SecretSyncPath,
) error {
	if direction != db.SecretSyncDirectionOutbound {
		return d.replaceSecretSyncPaths(syncID, paths)
	}
	existing, err := d.getSecretSyncPaths(syncID)
	if err != nil {
		return err
	}
	byID := make(map[int]db.SecretSyncPath, len(existing))
	for _, path := range existing {
		byID[path.ID] = path
	}
	kept := make(map[int]struct{}, len(paths))
	for _, path := range paths {
		current, exists := byID[path.ID]
		if exists {
			if current.AccessKeyID == path.AccessKeyID && current.Mount == path.Mount &&
				current.Path == path.Path && current.Field == path.Field {
				path.RemoteVersion = current.RemoteVersion
				path.ContentFingerprint = current.ContentFingerprint
			} else {
				path.RemoteVersion = 0
				path.ContentFingerprint = ""
			}
			if _, err = d.exec(
				"update project__secret_sync_path set path=?, prefix=?, `separator`=?, access_key_id=?, "+
					"mount=?, field=?, remote_version=?, content_fingerprint=? where id=? and sync_id=?",
				path.Path, path.Prefix, path.Separator, path.AccessKeyID, path.Mount, path.Field,
				path.RemoteVersion, path.ContentFingerprint, path.ID, syncID,
			); err != nil {
				return err
			}
			kept[path.ID] = struct{}{}
			continue
		}
		id, insertErr := d.insert(
			"id",
			"insert into project__secret_sync_path "+
				"(sync_id, path, prefix, `separator`, access_key_id, mount, field, remote_version, content_fingerprint) "+
				"values (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			syncID, path.Path, path.Prefix, path.Separator, path.AccessKeyID, path.Mount,
			path.Field, 0, "",
		)
		if insertErr != nil {
			return insertErr
		}
		kept[id] = struct{}{}
	}
	for _, path := range existing {
		if _, ok := kept[path.ID]; ok {
			continue
		}
		if _, err = d.exec("delete from project__secret_sync_path where id=? and sync_id=?", path.ID, syncID); err != nil {
			return err
		}
	}
	return nil
}
