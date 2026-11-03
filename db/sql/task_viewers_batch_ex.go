package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// taskViewerRole is a current member-role relation, loaded in one query.
type taskViewerRole struct {
	UserID         int                       `db:"user_id"`
	MemberRevision int                       `db:"member_revision"`
	RoleID         db.ProjectRoleID          `db:"assigned_role_id"`
	LDAP           *int                      `db:"ldap_assignment"`
	OIDC           *int                      `db:"oidc_assignment"`
	StoredID       *string                   `db:"stored_role_id"`
	Slug           *string                   `db:"role_slug"`
	RoleProjectID  *int                      `db:"role_project_id"`
	Permissions    *db.ProjectUserPermission `db:"role_permissions"`
	Revision       *int                      `db:"role_revision"`
}

func (d *SqlDb) batchTaskViewers(projectID, templateID int, workflowRunID *int) ([]int, error) {
	admins, err := d.GetAllAdmins()
	if err != nil {
		return nil, err
	}
	viewers := make([]int, 0, len(admins))
	seen := map[int]bool{}
	for _, admin := range admins {
		if !seen[admin.ID] {
			seen[admin.ID] = true
			viewers = append(viewers, admin.ID)
		}
	}
	var workflow db.WorkflowTemplate
	var grants []db.WorkflowRolePerm
	var overrides []db.TemplateRolePerm
	known := map[db.ProjectRoleReference]bool{}
	if workflowRunID != nil {
		var run struct {
			Snapshot   string `db:"definition_snapshot"`
			WorkflowID int    `db:"workflow_template_id"`
		}
		err = d.Sql().SelectOne(&run, d.PrepareQuery(`select wr.definition_snapshot,wr.workflow_template_id from project__workflow_run wr join project__workflow_template w on w.id=wr.workflow_template_id and w.project_id=wr.project_id where wr.project_id=? and wr.id=?`), projectID, *workflowRunID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return viewers, nil
		}
		if err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(run.Snapshot), &workflow) != nil || workflow.ID != run.WorkflowID || workflow.ProjectID != projectID {
			return viewers, nil
		}
		roles, roleErr := d.GetProjectRoles(projectID)
		if roleErr != nil {
			return nil, roleErr
		}
		ids := make([]db.ProjectRoleID, 0, len(roles))
		for _, role := range roles {
			if role.ProjectID == nil || *role.ProjectID != projectID || role.Revision < 1 {
				return viewers, nil
			}
			ids = append(ids, role.ID)
		}
		known = db.KnownProjectRoleReferences(ids)
		if _, err = d.selectAll(&grants, "select * from project__workflow_role where project_id=? and workflow_id=?", projectID, workflow.ID); err != nil {
			return nil, err
		}
	} else {
		count, countErr := d.Sql().SelectInt(d.PrepareQuery("select count(*) from project__template where project_id=? and id=?"), projectID, templateID)
		if countErr != nil {
			return nil, countErr
		}
		if count == 0 {
			return viewers, nil
		}
		if _, err = d.selectAll(&overrides, "select * from project__template_role where project_id=? and template_id=?", projectID, templateID); err != nil {
			return nil, err
		}
	}
	var rows []taskViewerRole
	_, err = d.selectAll(&rows, `select pur.user_id,pu.revision member_revision,pur.role_id assigned_role_id,
 pur.ldap_group_managed_assignment_id ldap_assignment,pur.oidc_group_managed_assignment_id oidc_assignment,
 r.role_id stored_role_id,r.slug role_slug,r.project_id role_project_id,r.permissions role_permissions,r.revision role_revision
 from project__user_role pur join project__user pu on pu.project_id=pur.project_id and pu.user_id=pur.user_id
 left join `+"`role`"+` r on r.role_id=pur.role_id and (r.project_id=pur.project_id or r.project_id is null)
 where pur.project_id=? order by pur.user_id,pur.role_id`, projectID)
	if err != nil {
		return nil, err
	}
	byUser := make(map[int][]taskViewerRole)
	for _, row := range rows {
		byUser[row.UserID] = append(byUser[row.UserID], row)
	}
	grantsByRole := make(map[db.ProjectRoleID]db.ProjectUserPermission)
	for _, grant := range grants {
		grantsByRole[grant.RoleID] |= grant.Permissions
	}
	for userID, assignments := range byUser {
		if seen[userID] {
			continue
		}
		managed := false
		for _, assignment := range assignments {
			if assignment.LDAP != nil || assignment.OIDC != nil {
				managed = true
				break
			}
		}
		if managed && workflowRunID != nil {
			state, stateErr := pro_interfaces.ResolveWorkflowObjectAuthorizationState(d, projectID, workflow.ID, userID)
			if stateErr == nil && pro_interfaces.AuthorizeWorkflowReadForState(workflow, state).Allowed {
				viewers = append(viewers, userID)
			}
			continue
		}
		var permissions db.ProjectUserPermission
		var allowed, denied db.TemplatePermission
		identities := make([]db.ProjectWorkflowRoleIdentity, 0, len(assignments))
		valid := true
		for _, assignment := range assignments {
			role := db.Role{ID: assignment.RoleID}
			if strings.HasPrefix(string(role.ID), "builtin:") {
				builtin := db.ProjectUserRole(strings.TrimPrefix(string(role.ID), "builtin:"))
				if !builtin.IsValid() || (workflowRunID != nil && assignment.MemberRevision < 1) {
					valid = false
					break
				}
				role.Slug = string(builtin)
				role.Permissions = builtin.GetPermissions()
				role.Builtin = true
				if workflowRunID != nil {
					ref, ok := db.ProjectRoleReferenceForBuiltInRole(builtin)
					if !ok {
						valid = false
						break
					}
					identities = append(identities, db.ProjectWorkflowRoleIdentity{Reference: ref, Permissions: role.Permissions, Revision: assignment.MemberRevision, Origin: db.ProjectWorkflowRoleOriginBuiltIn})
				}
			} else {
				if assignment.StoredID == nil || assignment.Slug == nil || assignment.Permissions == nil || assignment.Revision == nil || (workflowRunID != nil && *assignment.Revision < 1) {
					valid = false
					break
				}
				role.Slug = *assignment.Slug
				role.Permissions = *assignment.Permissions
				role.ProjectID = assignment.RoleProjectID
				if workflowRunID != nil && role.ProjectID != nil {
					identities = append(identities, db.ProjectWorkflowRoleIdentity{Reference: db.ProjectRoleReferenceForCustomRole(role.ID), Permissions: role.Permissions, Revision: *assignment.Revision, Origin: db.ProjectWorkflowRoleOriginManual})
				}
			}
			if workflowRunID == nil {
				permissions |= role.Permissions
				allowSlug := role.Builtin || (role.ProjectID == nil && !db.ProjectUserRole(strings.ToLower(role.Slug)).IsValid())
				for _, override := range overrides {
					if (override.RoleID != nil && *override.RoleID == role.ID) || (override.RoleID == nil && allowSlug && override.RoleSlug == role.Slug) {
						allowed |= override.AllowedPermissions
						denied |= override.DeniedPermissions
					}
				}
			} else {
				permissions |= grantsByRole[role.ID]
				if role.Builtin || role.ProjectID != nil {
					permissions |= role.Permissions
				}
			}
		}
		if !valid {
			continue
		}
		if workflowRunID == nil {
			if ((db.ProjectPermissionsToTemplate(permissions) | allowed) &^ denied).Can(db.CanReadTemplate) {
				viewers = append(viewers, userID)
			}
			continue
		}
		if len(identities) == 0 {
			continue
		}
		for _, identity := range identities {
			if identity.Validate() != nil {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		if permissions.Can(db.CanReadProject) && permissions&db.CanReadProject != 0 { // Only an object grant adds workflow view.
			var object db.ProjectUserPermission
			for _, assignment := range assignments {
				object |= grantsByRole[assignment.RoleID]
			}
			if object&db.CanReadProject != 0 {
				permissions |= db.CanViewWorkflows
			}
		}
		state := pro_interfaces.WorkflowAuthorizationState{Identities: identities, ProjectPermissions: permissions, KnownRoles: known}
		if pro_interfaces.AuthorizeWorkflowReadForState(workflow, state).Allowed {
			viewers = append(viewers, userID)
		}
	}
	return viewers, nil
}
