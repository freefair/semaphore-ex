package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-gorp/gorp/v3"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

const defaultTemplateVersionGrantPageSize = 50

func (d *WorkflowStoreImpl) PublishTemplateVersion(
	template db.Template,
	authorUserID int,
	created time.Time,
) (db.TemplateVersion, bool, error) {
	if d.connection == nil {
		return db.TemplateVersion{}, false, db.ErrNotFound
	}
	if template.ProjectID <= 0 || template.ID <= 0 || authorUserID <= 0 {
		return db.TemplateVersion{}, false, errors.New("template version publication ownership is invalid")
	}
	if created.IsZero() {
		created = time.Now().UTC()
	} else {
		created = created.UTC()
	}

	tx, err := d.connection.Begin()
	if err != nil {
		return db.TemplateVersion{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	version, reused, err := d.publishTemplateVersionTx(tx, template.ProjectID, template.ID, authorUserID, created, make(map[int]struct{}))
	if err != nil {
		_ = tx.Rollback()
		concurrent, concurrentErr := d.GetTemplateVersions(template.ProjectID, template.ID, db.RetrieveQueryParams{Count: 1})
		if concurrentErr == nil && len(concurrent) == 1 && version.ContentFingerprint != "" && concurrent[0].ContentFingerprint == version.ContentFingerprint {
			return concurrent[0], true, nil
		}
		return db.TemplateVersion{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return db.TemplateVersion{}, false, err
	}
	return version, reused, nil
}

func (d *WorkflowStoreImpl) publishTemplateVersionTx(
	tx *gorp.Transaction,
	ownerProjectID int,
	templateID int,
	authorUserID int,
	created time.Time,
	visiting map[int]struct{},
) (db.TemplateVersion, bool, error) {
	if _, cycle := visiting[templateID]; cycle {
		return db.TemplateVersion{}, false, errors.New("template version build dependency is cyclic")
	}
	visiting[templateID] = struct{}{}
	defer delete(visiting, templateID)
	template, err := loadTemplateForPublicationTx(tx, d, ownerProjectID, templateID)
	if err != nil {
		return db.TemplateVersion{}, false, err
	}
	snapshot, err := db.NewTemplateVersionSnapshot(template)
	if err != nil {
		return db.TemplateVersion{}, false, err
	}
	if template.BuildTemplateID != nil {
		buildVersion, _, buildErr := d.publishTemplateVersionTx(
			tx, ownerProjectID, *template.BuildTemplateID, authorUserID, created, visiting,
		)
		if buildErr != nil {
			return db.TemplateVersion{}, false, buildErr
		}
		snapshot.Dependencies.BuildTemplateVersion = &db.TemplateVersionReference{
			OwnerProjectID: buildVersion.OwnerProjectID, TemplateID: buildVersion.TemplateID,
			VersionNumber: buildVersion.VersionNumber, ContentFingerprint: buildVersion.ContentFingerprint,
		}
	}
	fingerprint, err := db.TemplateVersionFingerprint(snapshot)
	if err != nil {
		return db.TemplateVersion{}, false, err
	}
	payload, err := snapshot.CanonicalJSON()
	if err != nil {
		return db.TemplateVersion{}, false, err
	}
	latest, latestErr := d.latestTemplateVersionTx(tx, ownerProjectID, templateID)
	switch {
	case latestErr == nil && latest.ContentFingerprint == fingerprint:
		if err = latest.DecodeSnapshot(); err != nil {
			return db.TemplateVersion{}, false, err
		}
		return latest, true, nil
	case latestErr != nil && !errors.Is(latestErr, db.ErrNotFound):
		return db.TemplateVersion{}, false, latestErr
	}
	versionNumber := 1
	if latestErr == nil {
		versionNumber = latest.VersionNumber + 1
	}
	version := db.TemplateVersion{
		OwnerProjectID: ownerProjectID, TemplateID: templateID, VersionNumber: versionNumber,
		AuthorUserID: authorUserID, ContentFingerprint: fingerprint, SnapshotJSON: string(payload),
		Snapshot: snapshot, Created: created,
	}
	version.ID, err = d.insertTx(tx,
		"insert into project__template_version(owner_project_id,template_id,version_number,author_user_id,created,content_fingerprint,execution_snapshot) values (?,?,?,?,?,?,?)",
		version.OwnerProjectID, version.TemplateID, version.VersionNumber, version.AuthorUserID,
		version.Created, version.ContentFingerprint, version.SnapshotJSON,
	)
	if err != nil {
		return version, false, err
	}
	return version, false, nil
}

func (d *WorkflowStoreImpl) GetTemplateVersions(
	ownerProjectID int,
	templateID int,
	params db.RetrieveQueryParams,
) ([]db.TemplateVersion, error) {
	if d.connection == nil || ownerProjectID <= 0 || templateID <= 0 {
		return nil, db.ErrNotFound
	}
	pageSize, err := templateVersionGrantPageSize(params)
	if err != nil {
		return nil, err
	}
	query := "select * from project__template_version where owner_project_id=? and template_id=?"
	args := []any{ownerProjectID, templateID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	args = append(args, pageSize)
	var versions []db.TemplateVersion
	if _, err = d.connection.SelectAll(&versions, query, args...); err != nil {
		return nil, err
	}
	for index := range versions {
		if err = versions[index].DecodeSnapshot(); err != nil {
			return nil, err
		}
	}
	return versions, nil
}

func (d *WorkflowStoreImpl) GetTemplateVersion(
	ownerProjectID int,
	templateID int,
	versionNumber int,
) (db.TemplateVersion, error) {
	if d.connection == nil || ownerProjectID <= 0 || templateID <= 0 || versionNumber <= 0 {
		return db.TemplateVersion{}, db.ErrNotFound
	}
	var version db.TemplateVersion
	err := d.connection.SelectOne(&version,
		"select * from project__template_version where owner_project_id=? and template_id=? and version_number=?",
		ownerProjectID, templateID, versionNumber,
	)
	if err != nil {
		return db.TemplateVersion{}, err
	}
	if err = version.DecodeSnapshot(); err != nil {
		return db.TemplateVersion{}, err
	}
	return version, nil
}

func (d *WorkflowStoreImpl) DeleteTemplateVersion(ownerProjectID int, templateID int, versionNumber int) error {
	if d.connection == nil || ownerProjectID <= 0 || templateID <= 0 || versionNumber <= 0 {
		return db.ErrNotFound
	}
	var versionID int
	err := d.connection.SelectOne(&versionID,
		"select id from project__template_version where owner_project_id=? and template_id=? and version_number=?",
		ownerProjectID, templateID, versionNumber,
	)
	if err != nil {
		return err
	}
	if referenced, referenceErr := d.nonRevokedGrantVersionReferenceExists(versionID); referenceErr != nil {
		return referenceErr
	} else if referenced {
		return db.ErrCrossProjectTemplateGrantActiveReference
	}
	result, err := d.connection.Exec(
		"delete from project__template_version where id=?", versionID,
	)
	if err != nil {
		if referenced, referenceErr := d.nonRevokedGrantVersionReferenceExists(versionID); referenceErr != nil {
			return referenceErr
		} else if referenced {
			return db.ErrCrossProjectTemplateGrantActiveReference
		}
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 1 {
		return nil
	}
	return db.ErrNotFound
}

func (d *WorkflowStoreImpl) CreateCrossProjectTemplateGrant(
	grant db.CrossProjectTemplateGrant,
) (db.CrossProjectTemplateGrant, error) {
	if d.connection == nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if err := grant.Validate(); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if grant.Status != db.CrossProjectTemplateGrantPending || grant.Revision != 1 {
		return db.CrossProjectTemplateGrant{}, db.ErrCrossProjectTemplateGrantInvalidTransition
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureTemplateOwnershipTx(tx, d, grant.OwnerProjectID, grant.TemplateID); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	versionIDs, err := templateVersionIDsTx(tx, d, grant)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	grant.ID, err = d.insertTx(tx,
		"insert into project__cross_project_template_grant(owner_project_id,consumer_project_id,template_id,min_template_version,max_template_version,operations,status,revision,reason,created_by_user_id,created,accepted_by_user_id,accepted_at,revoked_by_user_id,revoked_at,revocation_reason) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		grant.OwnerProjectID, grant.ConsumerProjectID, grant.TemplateID,
		grant.MinTemplateVersion, grant.MaxTemplateVersion, grant.Operations, grant.Status, grant.Revision,
		grant.Reason, grant.CreatedByUserID, grant.Created.UTC(), grant.AcceptedByUserID, grant.AcceptedAt,
		grant.RevokedByUserID, grant.RevokedAt, grant.RevocationReason,
	)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if err = insertGrantVersionReferencesTx(tx, d, grant.ID, versionIDs); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	return grant, nil
}

func (d *WorkflowStoreImpl) GetCrossProjectTemplateGrant(projectID int, grantID int) (db.CrossProjectTemplateGrant, error) {
	if d.connection == nil || projectID <= 0 || grantID <= 0 {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	var grant db.CrossProjectTemplateGrant
	err := d.connection.SelectOne(&grant,
		"select * from project__cross_project_template_grant where id=? and (owner_project_id=? or consumer_project_id=?)",
		grantID, projectID, projectID,
	)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if err = grant.Validate(); err != nil {
		return db.CrossProjectTemplateGrant{}, fmt.Errorf("decode cross-project template grant: %w", err)
	}
	return grant, nil
}

func (d *WorkflowStoreImpl) GetMappedCrossProjectTemplateVersions(consumerProjectID int, grantID int, params db.RetrieveQueryParams) ([]db.TemplateVersion, db.CrossProjectTemplateGrant, error) {
	grant, err := d.GetCrossProjectTemplateGrant(consumerProjectID, grantID)
	if err != nil || grant.ConsumerProjectID != consumerProjectID || grant.Status != db.CrossProjectTemplateGrantActive || !grant.Operations.Allows(db.CrossProjectTemplateGrantReference) {
		return nil, db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	pageSize, err := templateVersionGrantPageSize(params)
	if err != nil {
		return nil, db.CrossProjectTemplateGrant{}, err
	}
	query := "select version.* from project__template_version version join project__cross_project_template_grant_version mapping on mapping.template_version_id=version.id where mapping.grant_id=?"
	args := []any{grantID}
	if params.BeforeID > 0 {
		query += " and version.id<?"
		args = append(args, params.BeforeID)
	}
	query += " order by version.id desc limit ?"
	args = append(args, pageSize)
	var versions []db.TemplateVersion
	if _, err = d.connection.SelectAll(&versions, query, args...); err != nil {
		return nil, db.CrossProjectTemplateGrant{}, err
	}
	for i := range versions {
		if err = versions[i].DecodeSnapshot(); err != nil {
			return nil, db.CrossProjectTemplateGrant{}, err
		}
	}
	return versions, grant, nil
}

// ResolveActiveCrossProjectTemplateGrant normalizes the two caller-controlled
// reference fields against the current accepted grant and its exact immutable
// version row. Server-derived provenance from a request is never trusted.
func (d *WorkflowStoreImpl) ResolveActiveCrossProjectTemplateGrant(
	consumerProjectID int,
	reference db.CrossProjectTemplateReference,
	operation db.CrossProjectTemplateGrantOperation,
) (db.CrossProjectTemplateReference, db.TemplateVersion, error) {
	if d.connection == nil || consumerProjectID <= 0 || reference.ValidateInput() != nil || !operation.IsValid() {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	normalized, version, err := d.resolveActiveCrossProjectTemplateGrantTx(tx, consumerProjectID, reference, operation)
	if err != nil {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, err
	}
	return normalized, version, nil
}

func (d *WorkflowStoreImpl) resolveActiveCrossProjectTemplateGrantTx(
	tx *gorp.Transaction,
	consumerProjectID int,
	reference db.CrossProjectTemplateReference,
	operation db.CrossProjectTemplateGrantOperation,
) (db.CrossProjectTemplateReference, db.TemplateVersion, error) {
	if err := reference.ValidateInput(); err != nil || consumerProjectID <= 0 || !operation.IsValid() {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, db.ErrNotFound
	}
	var grant db.CrossProjectTemplateGrant
	query := "select * from project__cross_project_template_grant where id=? and consumer_project_id=?"
	if d.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	err := tx.SelectOne(&grant, d.connection.PrepareQuery(query),
		reference.GrantID, consumerProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, db.ErrNotFound
	}
	if err != nil {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, err
	}
	decision := pro_interfaces.EvaluateCrossProjectTemplateGrant(grant, pro_interfaces.CrossProjectTemplateGrantRequest{
		ConsumerProjectID: consumerProjectID, TemplateID: grant.TemplateID, TemplateVersion: reference.TemplateVersionNumber,
		Operation: operation, ConsumerAuthorized: true,
	})
	if !decision.Allowed {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, db.ErrNotFound
	}
	var version db.TemplateVersion
	err = tx.SelectOne(&version, d.connection.PrepareQuery(
		"select version.* from project__template_version version join project__cross_project_template_grant_version grant_version on grant_version.template_version_id=version.id where grant_version.grant_id=? and version.owner_project_id=? and version.template_id=? and version.version_number=?"),
		grant.ID, grant.OwnerProjectID, grant.TemplateID, reference.TemplateVersionNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, db.ErrNotFound
	}
	if err != nil {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, err
	}
	if err = version.DecodeSnapshot(); err != nil {
		return db.CrossProjectTemplateReference{}, db.TemplateVersion{}, err
	}
	normalized := db.CrossProjectTemplateReference{
		GrantID: grant.ID, TemplateVersionNumber: version.VersionNumber,
		OwnerProjectID: version.OwnerProjectID, TemplateID: version.TemplateID, TemplateVersionID: version.ID,
		ContentFingerprint: version.ContentFingerprint, GrantRevision: grant.Revision,
	}
	return normalized, version, nil
}

func (d *WorkflowStoreImpl) UpdateCrossProjectTemplateGrant(
	ownerProjectID int,
	grantID int,
	update db.CrossProjectTemplateGrantUpdate,
	expectedRevision int,
) (db.CrossProjectTemplateGrant, error) {
	if d.connection == nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	defer func() { _ = tx.Rollback() }()
	grant, err := crossProjectTemplateGrantForOwnerTx(tx, d, ownerProjectID, grantID)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	updatedGrant, err := grant.Update(update, expectedRevision)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	versionIDs, err := templateVersionIDsTx(tx, d, updatedGrant)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__cross_project_template_grant set min_template_version=?, max_template_version=?, operations=?, reason=?, revision=? where id=? and owner_project_id=? and revision=? and status=?"),
		updatedGrant.MinTemplateVersion, updatedGrant.MaxTemplateVersion, updatedGrant.Operations,
		updatedGrant.Reason, updatedGrant.Revision, grantID, ownerProjectID, expectedRevision,
		db.CrossProjectTemplateGrantPending,
	)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if updated != 1 {
		return db.CrossProjectTemplateGrant{}, db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	if _, err = tx.Exec(d.connection.PrepareQuery(
		"delete from project__cross_project_template_grant_version where grant_id=?"), grantID); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if err = insertGrantVersionReferencesTx(tx, d, grantID, versionIDs); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	return updatedGrant, nil
}

func (d *WorkflowStoreImpl) DeleteCrossProjectTemplateGrant(ownerProjectID int, grantID int, expectedRevision int) error {
	if d.connection == nil {
		return db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	grant, err := crossProjectTemplateGrantForOwnerTx(tx, d, ownerProjectID, grantID)
	if err != nil {
		return err
	}
	if expectedRevision <= 0 || grant.Revision != expectedRevision {
		return db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	if grant.Status == db.CrossProjectTemplateGrantActive {
		return db.ErrCrossProjectTemplateGrantActiveReference
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"delete from project__cross_project_template_grant where id=? and owner_project_id=? and revision=? and status<>?"),
		grantID, ownerProjectID, expectedRevision, db.CrossProjectTemplateGrantActive,
	)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted != 1 {
		return db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	return tx.Commit()
}

func (d *WorkflowStoreImpl) GetCrossProjectTemplateGrants(
	projectID int,
	params db.RetrieveQueryParams,
) ([]db.CrossProjectTemplateGrant, error) {
	if d.connection == nil || projectID <= 0 {
		return nil, db.ErrNotFound
	}
	pageSize, err := templateVersionGrantPageSize(params)
	if err != nil {
		return nil, err
	}
	query := "select * from project__cross_project_template_grant where (owner_project_id=? or consumer_project_id=?)"
	args := []any{projectID, projectID}
	if params.BeforeID > 0 {
		query += " and id < ?"
		args = append(args, params.BeforeID)
	}
	query += " order by id desc limit ?"
	args = append(args, pageSize)
	var grants []db.CrossProjectTemplateGrant
	if _, err = d.connection.SelectAll(&grants, query, args...); err != nil {
		return nil, err
	}
	for index := range grants {
		if err = grants[index].Validate(); err != nil {
			return nil, fmt.Errorf("decode cross-project template grant: %w", err)
		}
	}
	return grants, nil
}

func (d *WorkflowStoreImpl) AcceptCrossProjectTemplateGrant(
	consumerProjectID int,
	grantID int,
	actorUserID int,
	expectedRevision int,
	acceptedAt time.Time,
) (db.CrossProjectTemplateGrant, error) {
	if d.connection == nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	defer func() { _ = tx.Rollback() }()
	grant, err := crossProjectTemplateGrantForConsumerTx(tx, d, consumerProjectID, grantID)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if grant.Revision != expectedRevision {
		return db.CrossProjectTemplateGrant{}, db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	if err = ensureGrantTemplateVersionReferencesTx(tx, d, grant); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	accepted, err := grant.Accept(actorUserID, expectedRevision, acceptedAt)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__cross_project_template_grant set status=?, revision=?, accepted_by_user_id=?, accepted_at=? where id=? and consumer_project_id=? and revision=?"),
		accepted.Status, accepted.Revision, accepted.AcceptedByUserID, accepted.AcceptedAt,
		grantID, consumerProjectID, expectedRevision,
	)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if updated != 1 {
		return db.CrossProjectTemplateGrant{}, db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	if err = tx.Commit(); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	return accepted, nil
}

func (d *WorkflowStoreImpl) RevokeCrossProjectTemplateGrant(
	projectID int,
	grantID int,
	actorUserID int,
	expectedRevision int,
	reason string,
	revokedAt time.Time,
) (db.CrossProjectTemplateGrant, error) {
	if d.connection == nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	defer func() { _ = tx.Rollback() }()
	grant, err := crossProjectTemplateGrantForProjectTx(tx, d, projectID, grantID)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if grant.Revision != expectedRevision {
		return db.CrossProjectTemplateGrant{}, db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	revoked, err := grant.Revoke(actorUserID, expectedRevision, reason, revokedAt)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	result, err := tx.Exec(d.connection.PrepareQuery(
		"update project__cross_project_template_grant set status=?, revision=?, revoked_by_user_id=?, revoked_at=?, revocation_reason=? where id=? and revision=? and (owner_project_id=? or consumer_project_id=?)"),
		revoked.Status, revoked.Revision, revoked.RevokedByUserID, revoked.RevokedAt, revoked.RevocationReason,
		grantID, expectedRevision, projectID, projectID,
	)
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if updated != 1 {
		return db.CrossProjectTemplateGrant{}, db.ErrCrossProjectTemplateGrantRevisionConflict
	}
	if _, err = tx.Exec(d.connection.PrepareQuery(
		"delete from project__cross_project_template_grant_version where grant_id=?"), grantID); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	return revoked, nil
}

func (d *WorkflowStoreImpl) HasActiveCrossProjectTemplateGrants(ownerProjectID int, templateID int) (bool, error) {
	if d.connection == nil || ownerProjectID <= 0 || templateID <= 0 {
		return false, db.ErrNotFound
	}
	var found int
	err := d.connection.SelectOne(&found,
		"select 1 from project__cross_project_template_grant where owner_project_id=? and template_id=? and status=? limit 1",
		ownerProjectID, templateID, db.CrossProjectTemplateGrantActive,
	)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// HasUnrevokedCrossProjectTemplateGrants protects owner template deletion.
// Pending grants are included: accepting one after the template disappeared
// would otherwise leave a live capability with no durable owner target.
func (d *WorkflowStoreImpl) HasUnrevokedCrossProjectTemplateGrants(ownerProjectID int, templateID int) (bool, error) {
	if d.connection == nil || ownerProjectID <= 0 || templateID <= 0 {
		return false, db.ErrNotFound
	}
	var found int
	err := d.connection.SelectOne(&found,
		"select 1 from project__cross_project_template_grant where owner_project_id=? and template_id=? and status<>? limit 1",
		ownerProjectID, templateID, db.CrossProjectTemplateGrantRevoked,
	)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// DeleteTemplateWithCrossProjectGrantGuard serializes owner deletion with
// grant creation by taking the same owner-template row lock first.
func (d *WorkflowStoreImpl) DeleteTemplateWithCrossProjectGrantGuard(ownerProjectID int, templateID int) error {
	if d.connection == nil || ownerProjectID <= 0 || templateID <= 0 {
		return db.ErrNotFound
	}
	tx, err := d.connection.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = ensureTemplateOwnershipTx(tx, d, ownerProjectID, templateID); err != nil {
		return err
	}
	var found int
	err = tx.SelectOne(&found, d.connection.PrepareQuery(
		"select 1 from project__cross_project_template_grant where owner_project_id=? and template_id=? and status<>? limit 1"),
		ownerProjectID, templateID, db.CrossProjectTemplateGrantRevoked)
	if err == nil {
		return db.ErrCrossProjectTemplateGrantUnrevokedReference
	}
	if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, db.ErrNotFound) {
		return err
	}
	result, err := tx.Exec(d.connection.PrepareQuery("delete from project__template where project_id=? and id=?"), ownerProjectID, templateID)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted != 1 {
		return db.ErrNotFound
	}
	return tx.Commit()
}

func (d *WorkflowStoreImpl) latestTemplateVersionTx(
	tx *gorp.Transaction,
	ownerProjectID int,
	templateID int,
) (db.TemplateVersion, error) {
	var version db.TemplateVersion
	err := tx.SelectOne(&version, d.connection.PrepareQuery(
		"select * from project__template_version where owner_project_id=? and template_id=? order by version_number desc limit 1"),
		ownerProjectID, templateID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return db.TemplateVersion{}, db.ErrNotFound
	}
	return version, err
}

// loadTemplateForPublicationTx reads the persisted owner definition inside the
// publication transaction. It intentionally does not fill TemplateVault.Vault,
// because that path resolves AccessKey material which must never enter a
// published template snapshot.
func loadTemplateForPublicationTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	ownerProjectID int,
	templateID int,
) (db.Template, error) {
	var template db.Template
	err := tx.SelectOne(&template, d.connection.PrepareQuery(
		"select * from project__template where project_id=? and id=?"), ownerProjectID, templateID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Template{}, db.ErrNotFound
	}
	if err != nil {
		return db.Template{}, err
	}
	if template.SurveyVarsJSON != nil && *template.SurveyVarsJSON != "" {
		if err = json.Unmarshal([]byte(*template.SurveyVarsJSON), &template.SurveyVars); err != nil {
			return db.Template{}, fmt.Errorf("decode template survey variables for version: %w", err)
		}
	}
	var environments []struct {
		EnvironmentID int `db:"environment_id"`
	}
	if _, err = tx.Select(&environments, d.connection.PrepareQuery(
		"select environment_id from project__template_environment where project_id=? and template_id=? order by environment_id"),
		ownerProjectID, templateID); err != nil {
		return db.Template{}, err
	}
	template.EnvironmentIDs = make([]int, 0, len(environments))
	for _, environment := range environments {
		template.EnvironmentIDs = append(template.EnvironmentIDs, environment.EnvironmentID)
	}
	template.ApplyLegacyEnvironmentField()
	if _, err = tx.Select(&template.Vaults, d.connection.PrepareQuery(
		"select * from project__template_vault where project_id=? and template_id=? order by id"), ownerProjectID, templateID); err != nil {
		return db.Template{}, err
	}
	return template, nil
}

func ensureTemplateOwnershipTx(tx *gorp.Transaction, d *WorkflowStoreImpl, ownerProjectID, templateID int) error {
	var found int
	query := "select id from project__template where project_id=? and id=?"
	if d.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	err := tx.SelectOne(&found, d.connection.PrepareQuery(query), ownerProjectID, templateID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.ErrNotFound
	}
	return err
}

func ensureTemplateVersionRangeTx(tx *gorp.Transaction, d *WorkflowStoreImpl, grant db.CrossProjectTemplateGrant) error {
	_, err := templateVersionIDsTx(tx, d, grant)
	return err
}

func templateVersionIDsTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	grant db.CrossProjectTemplateGrant,
) ([]int, error) {
	var versions []struct {
		ID int `db:"id"`
	}
	if _, err := tx.Select(&versions, d.connection.PrepareQuery(
		"select id from project__template_version where owner_project_id=? and template_id=? and version_number between ? and ? order by version_number"),
		grant.OwnerProjectID, grant.TemplateID, grant.MinTemplateVersion, grant.MaxTemplateVersion,
	); err != nil {
		return nil, err
	}
	expected := int64(grant.MaxTemplateVersion) - int64(grant.MinTemplateVersion) + 1
	if int64(len(versions)) != expected {
		return nil, db.ErrNotFound
	}
	ids := make([]int, 0, len(versions))
	for _, version := range versions {
		ids = append(ids, version.ID)
	}
	return ids, nil
}

func insertGrantVersionReferencesTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	grantID int,
	versionIDs []int,
) error {
	for _, versionID := range versionIDs {
		if _, err := tx.Exec(d.connection.PrepareQuery(
			"insert into project__cross_project_template_grant_version(grant_id,template_version_id) values (?,?)"),
			grantID, versionID,
		); err != nil {
			return err
		}
	}
	return nil
}

func ensureGrantTemplateVersionReferencesTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	grant db.CrossProjectTemplateGrant,
) error {
	versionIDs, err := templateVersionIDsTx(tx, d, grant)
	if err != nil {
		return err
	}
	var references []struct {
		TemplateVersionID int `db:"template_version_id"`
	}
	if _, err = tx.Select(&references, d.connection.PrepareQuery(
		"select template_version_id from project__cross_project_template_grant_version where grant_id=? order by template_version_id"), grant.ID); err != nil {
		return err
	}
	if len(references) != len(versionIDs) {
		return db.ErrNotFound
	}
	versionSet := make(map[int]struct{}, len(versionIDs))
	for _, versionID := range versionIDs {
		versionSet[versionID] = struct{}{}
	}
	for _, reference := range references {
		if _, ok := versionSet[reference.TemplateVersionID]; !ok {
			return db.ErrNotFound
		}
	}
	return nil
}

func (d *WorkflowStoreImpl) nonRevokedGrantVersionReferenceExists(templateVersionID int) (bool, error) {
	var found int
	err := d.connection.SelectOne(&found,
		"select 1 from project__cross_project_template_grant_version gtv join project__cross_project_template_grant grant on grant.id=gtv.grant_id where gtv.template_version_id=? and grant.status<>? limit 1",
		templateVersionID, db.CrossProjectTemplateGrantRevoked,
	)
	if errors.Is(err, db.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func crossProjectTemplateGrantForConsumerTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	consumerProjectID int,
	grantID int,
) (db.CrossProjectTemplateGrant, error) {
	var grant db.CrossProjectTemplateGrant
	query := "select * from project__cross_project_template_grant where id=? and consumer_project_id=?"
	if d.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	err := tx.SelectOne(&grant, d.connection.PrepareQuery(query), grantID, consumerProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if err == nil {
		err = grant.Validate()
	}
	return grant, err
}

func crossProjectTemplateGrantForOwnerTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	ownerProjectID int,
	grantID int,
) (db.CrossProjectTemplateGrant, error) {
	var grant db.CrossProjectTemplateGrant
	err := tx.SelectOne(&grant, d.connection.PrepareQuery(
		"select * from project__cross_project_template_grant where id=? and owner_project_id=?"), grantID, ownerProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if err == nil {
		err = grant.Validate()
	}
	return grant, err
}

func crossProjectTemplateGrantForProjectTx(
	tx *gorp.Transaction,
	d *WorkflowStoreImpl,
	projectID int,
	grantID int,
) (db.CrossProjectTemplateGrant, error) {
	var grant db.CrossProjectTemplateGrant
	query := "select * from project__cross_project_template_grant where id=? and (owner_project_id=? or consumer_project_id=?)"
	if d.connection.GetDialect() != util.DbDriverSQLite {
		query += " for update"
	}
	err := tx.SelectOne(&grant, d.connection.PrepareQuery(query),
		grantID, projectID, projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if err == nil {
		err = grant.Validate()
	}
	return grant, err
}

func templateVersionGrantPageSize(params db.RetrieveQueryParams) (int, error) {
	if params.Offset != 0 || params.Filter != "" || params.SortBy != "" || params.SortInverted {
		return 0, errors.New("template version and grant lists support keyset pagination only")
	}
	if params.Count <= 0 {
		return defaultTemplateVersionGrantPageSize, nil
	}
	if params.Count > db.MaxCrossProjectTemplateGrantPageSize {
		return 0, fmt.Errorf("template version and grant page exceeds %d entries", db.MaxCrossProjectTemplateGrantPageSize)
	}
	return params.Count, nil
}
