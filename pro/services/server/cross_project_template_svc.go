package server

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type unavailableCrossProjectTemplateService struct{}

func NewCrossProjectTemplateService(_ db.Store, _ any) pro_interfaces.CrossProjectTemplateService {
	return &unavailableCrossProjectTemplateService{}
}

func (*unavailableCrossProjectTemplateService) PublishTemplateVersion(int, int, *db.User) (db.TemplateVersion, bool, error) {
	return db.TemplateVersion{}, false, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) ListTemplateVersions(int, int, db.RetrieveQueryParams, *db.User) ([]db.TemplateVersion, error) {
	return nil, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) CreateGrant(int, int, pro_interfaces.CrossProjectTemplateGrantCreate, *db.User) (db.CrossProjectTemplateGrant, error) {
	return db.CrossProjectTemplateGrant{}, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) UpdateGrant(int, int, pro_interfaces.CrossProjectTemplateGrantUpdate, *db.User) (db.CrossProjectTemplateGrant, error) {
	return db.CrossProjectTemplateGrant{}, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) DeleteGrant(int, int, int, *db.User) (db.CrossProjectTemplateGrant, error) {
	return db.CrossProjectTemplateGrant{}, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) AcceptGrant(int, int, int, *db.User) (db.CrossProjectTemplateGrant, error) {
	return db.CrossProjectTemplateGrant{}, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) RevokeGrant(int, int, int, string, *db.User) (db.CrossProjectTemplateGrant, error) {
	return db.CrossProjectTemplateGrant{}, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) ListGrants(int, db.RetrieveQueryParams, *db.User) ([]db.CrossProjectTemplateGrant, error) {
	return nil, db.ErrNotFound
}
func (*unavailableCrossProjectTemplateService) ListReferences(int, int, db.RetrieveQueryParams, *db.User) ([]pro_interfaces.CrossProjectTemplateReferenceView, db.CrossProjectTemplateGrant, error) {
	return nil, db.CrossProjectTemplateGrant{}, db.ErrNotFound
}
