package sql

import (
	"errors"
	"github.com/semaphoreui/semaphore/db"
)

func (d *SqlDb) GetTemplatePermissionContext(
	projectID int,
	templateID int,
	userID int,
) (db.TemplatePermissionContext, error) {
	user, err := d.GetUser(userID)
	if err != nil {
		return db.TemplatePermissionContext{}, err
	}
	if user.Admin {
		return db.TemplatePermissionContext{
			RoleID: "built_in_administrator", RoleName: "Administrator",
			ProjectPermissions: db.ProjectOwner.GetPermissions(),
			EffectivePermissions: db.CanReadTemplate | db.CanRunTemplate |
				db.CanEditTemplate | db.CanDeleteTemplate,
		}, nil
	}

	projectUser, err := d.GetProjectUser(projectID, userID)
	if errors.Is(err, db.ErrNotFound) {
		return db.TemplatePermissionContext{}, nil
	}
	if err != nil {
		return db.TemplatePermissionContext{}, err
	}

	roleID := string(projectUser.Role)
	roleName := string(projectUser.Role)
	roleSlug := string(projectUser.Role)
	projectPermissions := projectUser.Role.GetPermissions()
	var customRoleID *db.ProjectRoleID
	if projectUser.RoleID != nil {
		role, roleErr := d.GetProjectRoleByID(projectID, *projectUser.RoleID)
		if roleErr != nil {
			return db.TemplatePermissionContext{}, roleErr
		}
		roleID = string(role.ID)
		roleName = role.Name
		roleSlug = role.Slug
		projectPermissions = role.Permissions
		customRoleID = &role.ID
	} else if !projectUser.Role.IsValid() {
		role, roleErr := d.GetProjectOrGlobalRoleBySlug(projectID, string(projectUser.Role))
		if errors.Is(roleErr, db.ErrNotFound) {
			return db.TemplatePermissionContext{}, nil
		}
		if roleErr != nil {
			return db.TemplatePermissionContext{}, roleErr
		}
		roleID = string(role.ID)
		roleName = role.Name
		roleSlug = role.Slug
		projectPermissions = role.Permissions
		if role.ProjectID != nil {
			customRoleID = &role.ID
		}
	}

	override, err := d.getTemplateRoleOverride(
		projectID, templateID, roleSlug, customRoleID,
	)
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return db.TemplatePermissionContext{}, err
	}
	var overridePtr *db.TemplateRolePerm
	if err == nil {
		overridePtr = &override
	}
	inherited := db.ProjectPermissionsToTemplate(projectPermissions)
	return db.TemplatePermissionContext{
		RoleID: roleID, RoleName: roleName, ProjectPermissions: projectPermissions,
		EffectivePermissions: db.ApplyTemplatePermissionOverride(inherited, overridePtr),
		Override:             overridePtr,
	}, nil
}

func (d *SqlDb) getTemplateRoleOverride(
	projectID int,
	templateID int,
	roleSlug string,
	roleID *db.ProjectRoleID,
) (db.TemplateRolePerm, error) {
	var override db.TemplateRolePerm
	if roleID != nil {
		err := d.selectOne(
			&override,
			"select * from project__template_role where project_id=? and template_id=? and role_id=?",
			projectID,
			templateID,
			*roleID,
		)
		return override, err
	}
	err := d.selectOne(
		&override,
		"select * from project__template_role where project_id=? and template_id=? and role_id is null and role_slug=?",
		projectID,
		templateID,
		roleSlug,
	)
	return override, err
}

func (d *SqlDb) normalizeTemplateRole(
	role db.TemplateRolePerm,
) (db.TemplateRolePerm, error) {
	if role.Revision <= 0 {
		role.Revision = 1
	}
	if role.AllowedPermissions == 0 && role.DeniedPermissions == 0 && role.Permissions != 0 {
		role.AllowedPermissions = db.ProjectPermissionsToTemplate(role.Permissions)
	}
	role.Permissions = db.TemplatePermissionsToProject(role.AllowedPermissions)
	if role.RoleID != nil {
		projectRole, err := d.GetProjectRoleByID(role.ProjectID, *role.RoleID)
		if err != nil {
			return db.TemplateRolePerm{}, err
		}
		role.RoleSlug = projectRole.Slug
	} else {
		roleRef := db.ProjectUserRole(role.RoleSlug)
		if !roleRef.IsValid() {
			resolved, err := d.GetProjectOrGlobalRoleBySlug(role.ProjectID, role.RoleSlug)
			if err != nil {
				return db.TemplateRolePerm{}, err
			}
			if resolved.ProjectID != nil {
				role.RoleID = &resolved.ID
			}
			role.RoleSlug = resolved.Slug
		}
	}
	if err := db.ValidateTemplateRolePerm(role); err != nil {
		return db.TemplateRolePerm{}, err
	}
	return role, nil
}
