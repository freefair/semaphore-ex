package sql

import "github.com/semaphoreui/semaphore/db"

type RoleMappingStoreImpl struct{}

func (d *RoleMappingStoreImpl) GetRoleMappings() ([]db.RoleMapping, error) {
	return []db.RoleMapping{}, nil
}
func (d *RoleMappingStoreImpl) CreateRoleMapping(mapping db.RoleMapping) (db.RoleMapping, error) {
	return mapping, db.ErrNotFound
}
func (d *RoleMappingStoreImpl) DeleteRoleMapping(int) (db.RoleMapping, []db.MappedRole, error) {
	return db.RoleMapping{}, nil, db.ErrNotFound
}
func (d *RoleMappingStoreImpl) SyncMappedRoles(int, string, []string) ([]db.MappedRole, []db.MappedRole, error) {
	return nil, nil, db.ErrInvalidOperation
}
