package db

import (
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"strings"
)

type ProjectRoleID string

// ValidateProjectRole validates the Enhanced project-scoped role contract.
// Slug remains an internal compatibility key; callers address roles by ID.
func ValidateProjectRole(role Role) error {
	if role.ProjectID == nil || *role.ProjectID <= 0 {
		return &common_errors.ValidationError{Message: "Project role requires a project"}
	}
	if strings.TrimSpace(string(role.ID)) == "" {
		return &common_errors.ValidationError{Message: "Project role ID cannot be empty"}
	}
	if strings.TrimSpace(role.Name) == "" {
		return &common_errors.ValidationError{Message: "Role name cannot be empty"}
	}
	if role.Revision <= 0 {
		return &common_errors.ValidationError{Message: "Project role revision must be positive"}
	}
	const knownPermissions = CanRunProjectTasks | CanUpdateProject | CanManageProjectResources |
		CanManageProjectUsers | CanViewProjectResources
	if role.Permissions&^knownPermissions != 0 {
		return &common_errors.ValidationError{Message: "Project role contains unknown permissions"}
	}
	return nil
}
