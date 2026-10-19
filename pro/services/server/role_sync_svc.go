package server

import (
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type RoleSyncServiceImpl struct{}

func NewRoleSyncService(db.RoleMappingStore) pro_interfaces.RoleSyncService {
	return &RoleSyncServiceImpl{}
}
func NewRoleSyncServiceWithOIDC(pro_interfaces.OIDCGroupMappingService) pro_interfaces.RoleSyncService {
	return &RoleSyncServiceImpl{}
}
func (*RoleSyncServiceImpl) SyncLoginRoles(int, string, map[string]any) ([]db.MappedRole, []db.MappedRole, error) {
	return nil, nil, db.ErrInvalidOperation
}
