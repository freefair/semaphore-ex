package server

import (
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type crossProjectTemplateService struct {
	store    db.Store
	versions db.TemplateVersionStore
	grants   db.CrossProjectTemplateGrantStore
}

func NewCrossProjectTemplateService(store db.Store, repository any) pro_interfaces.CrossProjectTemplateService {
	versions, _ := repository.(db.TemplateVersionStore)
	grants, _ := repository.(db.CrossProjectTemplateGrantStore)
	return &crossProjectTemplateService{store: store, versions: versions, grants: grants}
}

func (s *crossProjectTemplateService) requireAdmin(projectID int, actor *db.User) error {
	if actor == nil || actor.ID <= 0 {
		return db.ErrNotFound
	}
	current, err := s.store.GetUser(actor.ID)
	if err != nil {
		return db.ErrNotFound
	}
	if current.Admin {
		return nil
	}
	membership, err := s.store.GetProjectUser(projectID, current.ID)
	if err != nil || !membership.Role.GetPermissions().Can(db.CanManageProjectResources) {
		return db.ErrNotFound
	}
	return nil
}

func (s *crossProjectTemplateService) PublishTemplateVersion(projectID, templateID int, actor *db.User) (db.TemplateVersion, bool, error) {
	if s.versions == nil || s.requireAdmin(projectID, actor) != nil {
		return db.TemplateVersion{}, false, db.ErrNotFound
	}
	permission, err := s.store.GetTemplatePermissionContext(projectID, templateID, actor.ID)
	if err != nil || !permission.EffectivePermissions.Can(db.CanEditTemplate) {
		return db.TemplateVersion{}, false, db.ErrNotFound
	}
	template, err := s.store.GetTemplate(projectID, templateID)
	if err != nil {
		return db.TemplateVersion{}, false, db.ErrNotFound
	}
	return s.versions.PublishTemplateVersion(template, actor.ID, time.Now().UTC())
}

func (s *crossProjectTemplateService) ListTemplateVersions(projectID, templateID int, params db.RetrieveQueryParams, actor *db.User) ([]db.TemplateVersion, error) {
	if s.versions == nil || s.requireAdmin(projectID, actor) != nil {
		return nil, db.ErrNotFound
	}
	if _, err := s.store.GetTemplate(projectID, templateID); err != nil {
		return nil, db.ErrNotFound
	}
	return s.versions.GetTemplateVersions(projectID, templateID, params)
}

func (s *crossProjectTemplateService) CreateGrant(ownerProjectID, templateID int, command pro_interfaces.CrossProjectTemplateGrantCreate, actor *db.User) (db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.requireAdmin(ownerProjectID, actor) != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if _, err := s.store.GetTemplate(ownerProjectID, templateID); err != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if _, err := s.store.GetProject(command.ConsumerProjectID); err != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	return s.grants.CreateCrossProjectTemplateGrant(db.CrossProjectTemplateGrant{OwnerProjectID: ownerProjectID, ConsumerProjectID: command.ConsumerProjectID, TemplateID: templateID, MinTemplateVersion: command.MinVersion, MaxTemplateVersion: command.MaxVersion, Operations: command.Operations, Status: db.CrossProjectTemplateGrantPending, Revision: 1, Reason: command.Reason, CreatedByUserID: actor.ID, Created: time.Now().UTC()})
}

func (s *crossProjectTemplateService) UpdateGrant(ownerProjectID, grantID int, command pro_interfaces.CrossProjectTemplateGrantUpdate, actor *db.User) (db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.requireAdmin(ownerProjectID, actor) != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	return s.grants.UpdateCrossProjectTemplateGrant(ownerProjectID, grantID, db.CrossProjectTemplateGrantUpdate{MinTemplateVersion: command.MinVersion, MaxTemplateVersion: command.MaxVersion, Operations: command.Operations, Reason: command.Reason}, command.ExpectedRevision)
}

func (s *crossProjectTemplateService) DeleteGrant(ownerProjectID, grantID, expectedRevision int, actor *db.User) (db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.requireAdmin(ownerProjectID, actor) != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	grant, err := s.grants.GetCrossProjectTemplateGrant(ownerProjectID, grantID)
	if err != nil || grant.OwnerProjectID != ownerProjectID {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	if err = s.grants.DeleteCrossProjectTemplateGrant(ownerProjectID, grantID, expectedRevision); err != nil {
		return db.CrossProjectTemplateGrant{}, err
	}
	return grant, nil
}

func (s *crossProjectTemplateService) AcceptGrant(consumerProjectID, grantID, expectedRevision int, actor *db.User) (db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.requireAdmin(consumerProjectID, actor) != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	return s.grants.AcceptCrossProjectTemplateGrant(consumerProjectID, grantID, actor.ID, expectedRevision, time.Now().UTC())
}

func (s *crossProjectTemplateService) RevokeGrant(projectID, grantID, expectedRevision int, reason string, actor *db.User) (db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.requireAdmin(projectID, actor) != nil {
		return db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	return s.grants.RevokeCrossProjectTemplateGrant(projectID, grantID, actor.ID, expectedRevision, reason, time.Now().UTC())
}

func (s *crossProjectTemplateService) ListGrants(projectID int, params db.RetrieveQueryParams, actor *db.User) ([]db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.requireAdmin(projectID, actor) != nil {
		return nil, db.ErrNotFound
	}
	return s.grants.GetCrossProjectTemplateGrants(projectID, params)
}

func (s *crossProjectTemplateService) ListReferences(consumerProjectID, grantID int, params db.RetrieveQueryParams, actor *db.User) ([]pro_interfaces.CrossProjectTemplateReferenceView, db.CrossProjectTemplateGrant, error) {
	if s.grants == nil || s.versions == nil || s.requireAdmin(consumerProjectID, actor) != nil {
		return nil, db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	versions, grant, err := s.grants.GetMappedCrossProjectTemplateVersions(consumerProjectID, grantID, params)
	if err != nil {
		return nil, db.CrossProjectTemplateGrant{}, db.ErrNotFound
	}
	result := make([]pro_interfaces.CrossProjectTemplateReferenceView, 0, len(versions))
	for _, version := range versions {
		result = append(result, pro_interfaces.CrossProjectTemplateReferenceView{GrantID: grant.ID, GrantRevision: grant.Revision, OwnerProjectID: version.OwnerProjectID, TemplateID: version.TemplateID, TemplateVersionID: version.ID, TemplateVersionNumber: version.VersionNumber, ContentFingerprint: version.ContentFingerprint, Name: version.Snapshot.Execution.Name, Type: version.Snapshot.Execution.Type})
	}
	return result, grant, nil
}

var _ pro_interfaces.CrossProjectTemplateService = (*crossProjectTemplateService)(nil)
