package project

import (
	"strings"

	"github.com/semaphoreui/semaphore/db"
)

func (b *BackupDB) restoreEXTemplateGrant(templateID int, roleID db.ProjectRoleID, grant BackupTemplateRole) (db.TemplateRolePerm, error) {
	permission := db.TemplateRolePerm{TemplateID: templateID, ProjectID: b.meta.ID, RoleID: &roleID}
	if grant.AllowedPermissions != nil || grant.DeniedPermissions != nil {
		if grant.AllowedPermissions != nil {
			permission.AllowedPermissions = *grant.AllowedPermissions
		}
		if grant.DeniedPermissions != nil {
			permission.DeniedPermissions = *grant.DeniedPermissions
		}
	} else {
		// Unversioned EX exports used the compatibility project mask.
		permission.Permissions = grant.Permissions & db.KnownProjectRolePermissions
	}
	return b.store.CreateTemplateRole(permission)
}

func (b *BackupDB) workflowGrantRole(grant BackupWorkflowRole) (db.Role, error) {
	if !b.upstreamPermissions && grant.RoleID != "" {
		if id, ok := b.roleIDMap[grant.RoleID]; ok {
			for _, role := range b.roles {
				if role.ID == id {
					return role, nil
				}
			}
		}
		if grant.IsGlobal && strings.HasPrefix(string(grant.RoleID), "builtin:") {
			return b.store.GetGlobalRoleBySlug(strings.TrimPrefix(string(grant.RoleID), "builtin:"))
		}
		if grant.IsGlobal {
			// Reuse an existing instance role only by its exact immutable identity.
			return b.store.GetGlobalRoleByID(grant.RoleID)
		}
		// A custom role from another instance never resolves by a coincidental slug.
		return db.Role{}, db.ErrNotFound
	}
	if id, ok := b.roleSlugMap[grant.Slug]; ok {
		for _, role := range b.roles {
			if role.ID == id {
				return role, nil
			}
		}
	}
	role, err := b.store.GetGlobalRoleBySlug(grant.Slug)
	if err != nil {
		return db.Role{}, err
	}
	if !role.Builtin {
		return db.Role{}, db.ErrNotFound
	}
	return role, nil
}
