package sql

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
)

const globalCredentialVersionInsertColumns = "credential_id, version, fingerprint, material_kind, encrypted_material, external_reference, created_by_user_id, created"

const globalCredentialGrantProjectLimit = 200

func (d *SqlDb) CreateGlobalCredential(record db.GlobalCredential, version db.GlobalCredentialVersion) (db.GlobalCredential, db.GlobalCredentialVersion, error) {
	record.DisplayName = strings.TrimSpace(record.DisplayName)
	record.Revision = 1
	record.CurrentVersion = 1
	if record.Created.IsZero() {
		record.Created = time.Now().UTC()
	}
	record.Created = record.Created.UTC()
	record.Updated = record.Created
	if err := db.ValidateGlobalCredential(record); err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	version.Version = 1
	version.Created = record.Created
	fingerprint, err := db.NewGlobalCredentialVersionFingerprint()
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	version.Fingerprint = fingerprint
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	record.ID, err = insertGlobalCredentialTx(tx, d,
		"insert into global_credential (type, display_name, owner_user_id, enabled, revision, current_version, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?)",
		record.Type, record.DisplayName, record.OwnerUserID, record.Enabled, record.Revision, record.CurrentVersion, record.Created, record.Updated)
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	version.CredentialID = record.ID
	if err = db.ValidateGlobalCredentialVersion(version); err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	version.ID, err = insertGlobalCredentialTx(tx, d,
		"insert into global_credential_version ("+globalCredentialVersionInsertColumns+") values (?, ?, ?, ?, ?, ?, ?, ?)",
		version.CredentialID, version.Version, version.Fingerprint, version.MaterialKind, version.EncryptedMaterial, version.ExternalReference, version.CreatedByUserID, version.Created)
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	return record, version, nil
}

func (d *SqlDb) GetGlobalCredential(id int) (db.GlobalCredential, error) {
	var value db.GlobalCredential
	err := d.selectOne(&value, "select * from global_credential where id=?", id)
	return value, err
}

func (d *SqlDb) GetGlobalCredentials(params db.RetrieveQueryParams) ([]db.GlobalCredential, error) {
	if params.Offset < 0 || params.Count < 0 {
		return nil, db.ErrInvalidOperation
	}
	query := "select * from global_credential order by id desc"
	args := make([]any, 0, 2)
	if params.Count > 0 {
		query += " limit ?"
		args = append(args, params.Count)
	}
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	values := make([]db.GlobalCredential, 0)
	_, err := d.selectAll(&values, query, args...)
	return values, err
}

func (d *SqlDb) GetGlobalCredentialVersion(credentialID, versionNumber int) (db.GlobalCredentialVersion, error) {
	var value db.GlobalCredentialVersion
	err := d.selectOne(&value, "select * from global_credential_version where credential_id=? and version=?", credentialID, versionNumber)
	return value, err
}

func (d *SqlDb) GetGlobalCredentialVersions(credentialID int) ([]db.GlobalCredentialVersion, error) {
	values := make([]db.GlobalCredentialVersion, 0)
	_, err := d.selectAll(&values, "select * from global_credential_version where credential_id=? order by version desc limit 100", credentialID)
	return values, err
}

func (d *SqlDb) UpdateGlobalCredentialMetadata(record db.GlobalCredential, expectedRevision int, now time.Time) (db.GlobalCredential, error) {
	if record.ID <= 0 || record.Revision != expectedRevision || expectedRevision <= 0 || now.Location() != time.UTC {
		return db.GlobalCredential{}, db.ErrGlobalCredentialRevisionConflict
	}
	current, err := d.GetGlobalCredential(record.ID)
	if err != nil {
		return db.GlobalCredential{}, err
	}
	if current.Revision != expectedRevision {
		return db.GlobalCredential{}, db.ErrGlobalCredentialRevisionConflict
	}
	if record.Type != current.Type || record.OwnerUserID != current.OwnerUserID || record.CurrentVersion != current.CurrentVersion {
		return db.GlobalCredential{}, db.ErrInvalidOperation
	}
	name := strings.TrimSpace(record.DisplayName)
	if name == "" || len(name) > 128 {
		return db.GlobalCredential{}, db.ErrInvalidOperation
	}
	result, err := d.exec("update global_credential set display_name=?, revision=revision+1, updated=? where id=? and revision=?", name, now, record.ID, expectedRevision)
	if err != nil {
		return db.GlobalCredential{}, err
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialRevisionConflict); err != nil {
		return db.GlobalCredential{}, err
	}
	return d.GetGlobalCredential(record.ID)
}

func (d *SqlDb) SetGlobalCredentialEnabled(id int, enabled bool, expectedRevision int, now time.Time) (db.GlobalCredential, error) {
	if id <= 0 || expectedRevision <= 0 || now.Location() != time.UTC {
		return db.GlobalCredential{}, db.ErrGlobalCredentialRevisionConflict
	}
	result, err := d.exec("update global_credential set enabled=?, revision=revision+1, updated=? where id=? and revision=?", enabled, now, id, expectedRevision)
	if err != nil {
		return db.GlobalCredential{}, err
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialRevisionConflict); err != nil {
		return db.GlobalCredential{}, err
	}
	return d.GetGlobalCredential(id)
}

func (d *SqlDb) RotateGlobalCredential(id, expectedRevision int, version db.GlobalCredentialVersion, now time.Time) (db.GlobalCredential, db.GlobalCredentialVersion, error) {
	if id <= 0 || expectedRevision <= 0 || now.Location() != time.UTC {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, db.ErrGlobalCredentialRevisionConflict
	}
	tx, err := d.Sql().Begin()
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := getGlobalCredentialTx(tx, d, id)
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	if current.Revision != expectedRevision {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, db.ErrGlobalCredentialRevisionConflict
	}
	version.ID, version.CredentialID, version.Version, version.Created = 0, id, current.CurrentVersion+1, now
	version.Fingerprint, err = db.NewGlobalCredentialVersionFingerprint()
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	if err = db.ValidateGlobalCredentialVersion(version); err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	result, err := tx.Exec(d.PrepareQuery("update global_credential set current_version=?, revision=revision+1, updated=? where id=? and revision=?"), version.Version, now, id, expectedRevision)
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialRevisionConflict); err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	// Claim the next version through the header CAS before inserting it. A
	// competing rotation therefore returns the stable revision conflict rather
	// than exposing the version table's unique constraint.
	version.ID, err = insertGlobalCredentialTx(tx, d, "insert into global_credential_version ("+globalCredentialVersionInsertColumns+") values (?, ?, ?, ?, ?, ?, ?, ?)", version.CredentialID, version.Version, version.Fingerprint, version.MaterialKind, version.EncryptedMaterial, version.ExternalReference, version.CreatedByUserID, version.Created)
	if err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.GlobalCredential{}, db.GlobalCredentialVersion{}, err
	}
	updated, err := d.GetGlobalCredential(id)
	return updated, version, err
}

func (d *SqlDb) CreateGlobalCredentialGrant(grant db.GlobalCredentialGrant) (db.GlobalCredentialGrant, error) {
	if grant.Status != db.GlobalCredentialGrantStatusActive || grant.RevokedByUserID != nil || grant.RevokedAt != nil {
		return db.GlobalCredentialGrant{}, db.ErrInvalidOperation
	}
	grant.Revision = 1
	if grant.Created.IsZero() {
		grant.Created = time.Now().UTC()
	}
	grant.Created, grant.Updated = grant.Created.UTC(), grant.Created.UTC()
	if err := db.ValidateGlobalCredentialGrant(grant); err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	if _, err := d.GetGlobalCredential(grant.CredentialID); err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	grantID, err := d.insert("id", "insert into global_credential_grant (credential_id, project_id, operations, expires_at, status, revision, created_by_user_id, revoked_by_user_id, revoked_at, created, updated) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", grant.CredentialID, grant.ProjectID, grant.Operations, grant.ExpiresAt, grant.Status, grant.Revision, grant.CreatedByUserID, grant.RevokedByUserID, grant.RevokedAt, grant.Created, grant.Updated)
	if err != nil {
		if isUniqueConstraint(err) {
			return db.GlobalCredentialGrant{}, db.ErrGlobalCredentialGrantExists
		}
		if isForeignKeyConstraint(err) {
			return db.GlobalCredentialGrant{}, db.ErrGlobalCredentialGrantDependency
		}
		return db.GlobalCredentialGrant{}, err
	}
	grant.ID = grantID
	return d.GetGlobalCredentialGrant(grant.CredentialID, grant.ID)
}

func (d *SqlDb) GetGlobalCredentialGrant(credentialID, grantID int) (db.GlobalCredentialGrant, error) {
	var value db.GlobalCredentialGrant
	err := d.selectOne(&value, "select * from global_credential_grant where credential_id=? and id=?", credentialID, grantID)
	return value, err
}

func (d *SqlDb) GetGlobalCredentialGrants(credentialID int, params db.RetrieveQueryParams) ([]db.GlobalCredentialGrant, error) {
	if credentialID <= 0 || params.Offset < 0 || params.Count < 0 {
		return nil, db.ErrInvalidOperation
	}
	values := make([]db.GlobalCredentialGrant, 0)
	query := "select * from global_credential_grant where credential_id=? order by id"
	args := []any{credentialID}
	if params.Count > 0 {
		query += " limit ?"
		args = append(args, params.Count)
	}
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	_, err := d.selectAll(&values, query, args...)
	return values, err
}

func (d *SqlDb) UpdateGlobalCredentialGrant(grant db.GlobalCredentialGrant, expectedRevision int, now time.Time) (db.GlobalCredentialGrant, error) {
	if grant.ID <= 0 || grant.Revision != expectedRevision || expectedRevision <= 0 || now.Location() != time.UTC {
		return db.GlobalCredentialGrant{}, db.ErrGlobalCredentialGrantConflict
	}
	current, err := d.GetGlobalCredentialGrant(grant.CredentialID, grant.ID)
	if err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	if current.Revision != expectedRevision {
		return db.GlobalCredentialGrant{}, db.ErrGlobalCredentialGrantConflict
	}
	if grant.ProjectID != current.ProjectID || grant.Status != current.Status || grant.CreatedByUserID != current.CreatedByUserID || !sameOptionalInt(grant.RevokedByUserID, current.RevokedByUserID) || !sameOptionalTime(grant.RevokedAt, current.RevokedAt) {
		return db.GlobalCredentialGrant{}, db.ErrInvalidOperation
	}
	current.Operations, current.ExpiresAt = grant.Operations, grant.ExpiresAt
	if err = db.ValidateGlobalCredentialGrant(current); err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	result, err := d.exec("update global_credential_grant set operations=?, expires_at=?, revision=revision+1, updated=? where credential_id=? and id=? and revision=?", current.Operations, current.ExpiresAt, now, current.CredentialID, current.ID, expectedRevision)
	if err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialGrantConflict); err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	return d.GetGlobalCredentialGrant(grant.CredentialID, grant.ID)
}

func (d *SqlDb) SetGlobalCredentialGrantStatus(credentialID, grantID int, status db.GlobalCredentialGrantStatus, expectedRevision int, revokedByUserID *int, now time.Time) (db.GlobalCredentialGrant, error) {
	if expectedRevision <= 0 || now.Location() != time.UTC || (status != db.GlobalCredentialGrantStatusActive && status != db.GlobalCredentialGrantStatusRevoked) {
		return db.GlobalCredentialGrant{}, db.ErrGlobalCredentialGrantConflict
	}
	current, err := d.GetGlobalCredentialGrant(credentialID, grantID)
	if err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	if current.Revision != expectedRevision {
		return db.GlobalCredentialGrant{}, db.ErrGlobalCredentialGrantConflict
	}
	if current.Status == status {
		return db.GlobalCredentialGrant{}, db.ErrInvalidOperation
	}
	if status == db.GlobalCredentialGrantStatusRevoked {
		if revokedByUserID == nil || *revokedByUserID <= 0 {
			return db.GlobalCredentialGrant{}, db.ErrInvalidOperation
		}
		current.RevokedByUserID, current.RevokedAt = revokedByUserID, &now
	} else {
		current.RevokedByUserID, current.RevokedAt = nil, nil
	}
	current.Status = status
	if err = db.ValidateGlobalCredentialGrant(current); err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	result, err := d.exec("update global_credential_grant set status=?, revoked_by_user_id=?, revoked_at=?, revision=revision+1, updated=? where credential_id=? and id=? and revision=?", current.Status, current.RevokedByUserID, current.RevokedAt, now, credentialID, grantID, expectedRevision)
	if err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialGrantConflict); err != nil {
		return db.GlobalCredentialGrant{}, err
	}
	return d.GetGlobalCredentialGrant(credentialID, grantID)
}

func (d *SqlDb) DeleteGlobalCredentialGrant(credentialID, grantID, expectedRevision int) error {
	grant, err := d.GetGlobalCredentialGrant(credentialID, grantID)
	if err != nil {
		return err
	}
	if expectedRevision <= 0 || grant.Revision != expectedRevision {
		return db.ErrGlobalCredentialGrantConflict
	}
	if grant.Status != db.GlobalCredentialGrantStatusRevoked {
		return db.ErrInvalidOperation
	}
	result, err := d.exec("delete from global_credential_grant where credential_id=? and id=? and revision=?", credentialID, grantID, expectedRevision)
	if err != nil {
		return err
	}
	return requireOneRow(result, db.ErrGlobalCredentialGrantConflict)
}

func (d *SqlDb) GetEffectiveGlobalCredentialMetadata(projectID int, now time.Time, params db.RetrieveQueryParams) ([]db.GlobalCredentialGrantedMetadata, error) {
	if projectID <= 0 || now.Location() != time.UTC || params.Offset < 0 || params.Count < 0 {
		return nil, db.ErrInvalidOperation
	}
	values := make([]db.GlobalCredentialGrantedMetadata, 0)
	query := "select c.id credential_id, c.type, c.display_name, c.current_version version, g.operations, g.id grant_id, g.revision grant_revision, g.expires_at from global_credential_grant g join global_credential c on c.id=g.credential_id where g.project_id=? and c.enabled=? and g.status=? and (g.operations & ?) = ? and (g.expires_at is null or g.expires_at>?) order by c.display_name, c.id"
	args := []any{projectID, true, db.GlobalCredentialGrantStatusActive, db.GlobalCredentialGrantOperationReference, db.GlobalCredentialGrantOperationReference, now}
	if params.Count > 0 {
		query += " limit ?"
		args = append(args, params.Count)
	}
	if params.Offset > 0 {
		query += " offset ?"
		args = append(args, params.Offset)
	}
	_, err := d.selectAll(&values, query, args...)
	return values, err
}

func (d *SqlDb) GetGlobalCredentialGrantProjects() ([]db.GlobalCredentialGrantProject, error) {
	values := make([]db.GlobalCredentialGrantProject, 0)
	_, err := d.selectAll(&values, "select id, name from project order by name, id limit ?", globalCredentialGrantProjectLimit)
	return values, err
}

func (d *SqlDb) DeleteGlobalCredential(id, expectedRevision int) error {
	tx, err := d.Sql().Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := getGlobalCredentialTx(tx, d, id)
	if err != nil {
		return err
	}
	if expectedRevision <= 0 || record.Revision != expectedRevision {
		return db.ErrGlobalCredentialRevisionConflict
	}
	if record.Enabled {
		return db.ErrGlobalCredentialEnabled
	}
	// Keep the grant guard in the same delete predicate. This closes the gap
	// between a prior count and delete when another transaction creates a grant.
	result, err := tx.Exec(d.PrepareQuery("delete from global_credential where id=? and revision=? and enabled=? and not exists (select 1 from global_credential_grant where credential_id=?)"), id, expectedRevision, false, id)
	if err != nil {
		return mapGlobalCredentialDeleteError(err)
	}
	if err = requireOneRow(result, db.ErrGlobalCredentialRevisionConflict); err != nil {
		grants, countErr := tx.SelectInt(d.PrepareQuery("select count(1) from global_credential_grant where credential_id=?"), id)
		if countErr != nil {
			return countErr
		}
		if grants != 0 {
			return db.ErrGlobalCredentialGrantsExist
		}
		return err
	}
	return tx.Commit()
}

func getGlobalCredentialTx(tx *gorp.Transaction, d *SqlDb, id int) (db.GlobalCredential, error) {
	var value db.GlobalCredential
	err := tx.SelectOne(&value, d.PrepareQuery("select * from global_credential where id=?"), id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.GlobalCredential{}, db.ErrNotFound
	}
	return value, err
}

func insertGlobalCredentialTx(tx *gorp.Transaction, d *SqlDb, query string, args ...any) (int, error) {
	if d.GetDialect() == util.DbDriverPostgres {
		id, err := tx.SelectInt(d.PrepareQuery(query+" returning id"), args...)
		return int(id), err
	}
	result, err := tx.Exec(d.PrepareQuery(query), args...)
	if err != nil {
		return 0, err
	}
	id, err := result.LastInsertId()
	return int(id), err
}

func requireOneRow(result sql.Result, conflict error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return conflict
	}
	return nil
}

func isUniqueConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "duplicate")
}

func isForeignKeyConstraint(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "foreign key") || strings.Contains(message, "cannot delete or update a parent row")
}

func mapGlobalCredentialDeleteError(err error) error {
	if isForeignKeyConstraint(err) {
		return db.ErrGlobalCredentialGrantsExist
	}
	return err
}

func sameOptionalInt(left, right *int) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func sameOptionalTime(left, right *time.Time) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && left.Equal(*right))
}
