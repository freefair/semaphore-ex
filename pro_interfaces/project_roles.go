package pro_interfaces

import "github.com/semaphoreui/semaphore/db"

// PermissionID is a stable API identifier for one assignable permission.
type PermissionID string

const (
	PermissionRunProjectTasks        PermissionID = "project.tasks.run"
	PermissionUpdateProject          PermissionID = "project.settings.update"
	PermissionViewProjectResources   PermissionID = "project.resources.view"
	PermissionManageProjectResources PermissionID = "project.resources.manage"
	PermissionManageProjectUsers     PermissionID = "project.members.manage"
)

type PermissionScope string

const PermissionScopeProject PermissionScope = "project"

// PermissionDefinition is the backend-authoritative catalog entry presented
// to API and UI clients. Capability prerequisites are typed even when a core
// permission has none, so later Enhanced permissions cannot rely on UI-only
// availability checks.
type PermissionDefinition struct {
	ID                      PermissionID             `json:"id"`
	Description             string                   `json:"description"`
	Scope                   PermissionScope          `json:"scope"`
	Permission              db.ProjectUserPermission `json:"permission"`
	CapabilityPrerequisites []CapabilityID           `json:"capability_prerequisites"`
}

// ProjectPermissionCatalog returns an isolated, stable-order catalog.
func ProjectPermissionCatalog() []PermissionDefinition {
	definitions := []PermissionDefinition{
		{
			ID: PermissionRunProjectTasks, Description: "Run project tasks",
			Scope: PermissionScopeProject, Permission: db.CanRunProjectTasks,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionUpdateProject, Description: "Update project settings",
			Scope: PermissionScopeProject, Permission: db.CanUpdateProject,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionViewProjectResources, Description: "View project resources",
			Scope: PermissionScopeProject, Permission: db.CanViewProjectResources,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID:          PermissionManageProjectResources,
			Description: "Create, update, and delete project resources",
			Scope:       PermissionScopeProject, Permission: db.CanManageProjectResources,
			CapabilityPrerequisites: []CapabilityID{},
		},
		{
			ID: PermissionManageProjectUsers, Description: "Manage project members and roles",
			Scope: PermissionScopeProject, Permission: db.CanManageProjectUsers,
			CapabilityPrerequisites: []CapabilityID{},
		},
	}
	result := make([]PermissionDefinition, len(definitions))
	copy(result, definitions)
	return result
}
