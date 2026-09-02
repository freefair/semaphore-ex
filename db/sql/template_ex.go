package sql

import (
	"errors"
	sq "github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"strings"
)

type templateWithLastTask struct {
	db.TemplateWithPerms
	LastTaskID *int `db:"last_task_id"`
}

const templateSearchVisibleIDChunkSize = 500

// filterVisibleTemplateSearchCandidates narrows already-authorized templates
// with a portable SQL LIKE query. The caller has applied the canonical
// per-template evaluator before passing IDs here; this function must never
// become a second permission implementation.
func (d *SqlDb) filterVisibleTemplateSearchCandidates(
	baseQuery sq.SelectBuilder,
	visible []templateWithLastTask,
	normalizedSearch string,
	hasRunnerTagPolicy bool,
	withPermissions bool,
) ([]templateWithLastTask, error) {
	if len(visible) == 0 {
		return visible, nil
	}

	permissionsByID := make(map[int]*db.ProjectUserPermission, len(visible))
	visibleIDs := make([]int, 0, len(visible))
	for _, template := range visible {
		visibleIDs = append(visibleIDs, template.ID)
		permissionsByID[template.ID] = template.Permissions
	}

	pattern := templateSearchSQLPattern(normalizedSearch)
	candidates := make([]templateWithLastTask, 0, len(visible))
	for start := 0; start < len(visibleIDs); start += templateSearchVisibleIDChunkSize {
		end := start + templateSearchVisibleIDChunkSize
		if end > len(visibleIDs) {
			end = len(visibleIDs)
		}
		query, args, err := baseQuery.
			Where(sq.Eq{"pt.id": visibleIDs[start:end]}).
			Where(templateSearchSQLCondition(pattern, hasRunnerTagPolicy)).
			ToSql()
		if err != nil {
			return nil, err
		}

		var chunk []templateWithLastTask
		if _, err = d.selectAll(&chunk, query, args...); err != nil {
			return nil, err
		}
		if withPermissions {
			for i := range chunk {
				chunk[i].Permissions = permissionsByID[chunk[i].ID]
			}
		}
		candidates = append(candidates, chunk...)
	}

	return candidates, nil
}

func templateSearchCanUseSQLCandidateFilter(normalizedSearch string) bool {
	for _, char := range normalizedSearch {
		if char < 0x20 || char == 0x7f || char > 0x7e {
			return false
		}
	}
	return true
}

func templateSearchSQLPattern(normalizedSearch string) string {
	escaped := strings.NewReplacer(
		"!", "!!",
		"%", "!%",
		"_", "!_",
	).Replace(normalizedSearch)
	return "%" + escaped + "%"
}

func templateSearchSQLCondition(pattern string, includeRunnerTags bool) sq.Sqlizer {
	conditions := sq.Or{
		sq.Expr("LOWER(pt.name) LIKE ? ESCAPE '!'", pattern),
		sq.Expr("LOWER(pt.description) LIKE ? ESCAPE '!'", pattern),
		sq.Expr("LOWER(pt.playbook) LIKE ? ESCAPE '!'", pattern),
		sq.Expr("LOWER(pt.runner_tag) LIKE ? ESCAPE '!'", pattern),
	}
	if includeRunnerTags {
		conditions = append(conditions, sq.Expr("LOWER(pt.runner_tags) LIKE ? ESCAPE '!'", pattern))
	}
	return conditions
}

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
