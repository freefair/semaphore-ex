package sql

import (
	"encoding/json"
	"errors"
	sq "github.com/Masterminds/squirrel"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	log "github.com/sirupsen/logrus"
)

// validateTemplateNameIsFree rejects a template name which is already used by
// another template of the same project, so that a template can be referred to by
// name. templateID is the template being updated, or 0 when creating one.
//
// Templates created before this check may still share a name, which is why
// GetTemplateByName rejects an ambiguous name rather than relying on this.
func (d *SqlDb) validateTemplateNameIsFree(projectID int, templateID int, name string) error {
	var count int

	err := d.selectOne(&count,
		"select count(*) from project__template where project_id=? and name=? and id<>?",
		projectID, name, templateID)

	if err != nil {
		return err
	}

	if count > 0 {
		return common_errors.NewValidationError("template with name " + name + " already exists")
	}

	return nil
}

func (d *SqlDb) CreateTemplate(tmpl db.Template) (db.Template, error) {
	if err := tmpl.Validate(); err != nil {
		return db.Template{}, err
	}

	if err := d.validateTemplateNameIsFree(tmpl.ProjectID, 0, tmpl.Name); err != nil {
		return db.Template{}, err
	}

	tmpl.ApplyLegacyEnvironmentField()

	fields := map[string]any{
		"project_id":                    tmpl.ProjectID,
		"inventory_id":                  tmpl.InventoryID,
		"repository_id":                 tmpl.RepositoryID,
		"name":                          tmpl.Name,
		"playbook":                      tmpl.Playbook,
		"working_directory":             tmpl.WorkingDirectory,
		"arguments":                     tmpl.Arguments,
		"allow_override_args_in_task":   tmpl.AllowOverrideArgsInTask,
		"description":                   tmpl.Description,
		"`type`":                        tmpl.Type,
		"start_version":                 tmpl.StartVersion,
		"build_template_id":             tmpl.BuildTemplateID,
		"view_id":                       tmpl.ViewID,
		"autorun":                       tmpl.Autorun,
		"survey_vars":                   db.ObjectToJSON(tmpl.SurveyVars),
		"suppress_success_alerts":       tmpl.SuppressSuccessAlerts,
		"suppress_error_alerts":         tmpl.SuppressErrorAlerts,
		"app":                           tmpl.App,
		"git_branch":                    tmpl.GitBranch,
		"runner_tag":                    tmpl.RunnerTag,
		"task_params":                   tmpl.TaskParams,
		"allow_override_branch_in_task": tmpl.AllowOverrideBranchInTask,
		"allow_parallel_tasks":          tmpl.AllowParallelTasks,
		"jwt_params":                    tmpl.JWTParams,
		"executor_image":                tmpl.NormalizedExecutorImage(),
	}
	if err := d.addTemplateRunnerTagPolicyFields(fields, tmpl); err != nil {
		return db.Template{}, err
	}

	query, args, err := sq.Insert("project__template").
		SetMap(fields).
		ToSql()
	if err != nil {
		return db.Template{}, err
	}

	tmplId, err := d.insert("id", query, args...)
	if err != nil {
		return db.Template{}, err
	}

	err = d.UpdateTemplateVaults(tmpl.ProjectID, tmplId, tmpl.Vaults)
	if err != nil {
		return db.Template{}, err
	}

	err = d.UpdateTemplateEnvironments(tmpl.ProjectID, tmplId, tmpl.EnvironmentIDs)
	if err != nil {
		return db.Template{}, err
	}

	tmpl.ID = tmplId
	if err = db.FillTemplate(d, &tmpl); err != nil {
		return db.Template{}, err
	}

	return tmpl, nil
}

func (d *SqlDb) UpdateTemplate(tmpl db.Template) error {
	err := tmpl.Validate()
	if err != nil {
		return err
	}

	if err = d.validateTemplateNameIsFree(tmpl.ProjectID, tmpl.ID, tmpl.Name); err != nil {
		return err
	}

	fields := map[string]any{
		"inventory_id":                  tmpl.InventoryID,
		"repository_id":                 tmpl.RepositoryID,
		"name":                          tmpl.Name,
		"playbook":                      tmpl.Playbook,
		"working_directory":             tmpl.WorkingDirectory,
		"arguments":                     tmpl.Arguments,
		"allow_override_args_in_task":   tmpl.AllowOverrideArgsInTask,
		"description":                   tmpl.Description,
		"`type`":                        tmpl.Type,
		"start_version":                 tmpl.StartVersion,
		"build_template_id":             tmpl.BuildTemplateID,
		"view_id":                       tmpl.ViewID,
		"autorun":                       tmpl.Autorun,
		"survey_vars":                   db.ObjectToJSON(tmpl.SurveyVars),
		"suppress_success_alerts":       tmpl.SuppressSuccessAlerts,
		"suppress_error_alerts":         tmpl.SuppressErrorAlerts,
		"app":                           tmpl.App,
		"`git_branch`":                  tmpl.GitBranch,
		"task_params":                   tmpl.TaskParams,
		"runner_tag":                    tmpl.RunnerTag,
		"allow_override_branch_in_task": tmpl.AllowOverrideBranchInTask,
		"allow_parallel_tasks":          tmpl.AllowParallelTasks,
		"jwt_params":                    tmpl.JWTParams,
		"executor_image":                tmpl.NormalizedExecutorImage(),
	}
	if err := d.addTemplateRunnerTagPolicyFields(fields, tmpl); err != nil {
		return err
	}

	query, args, err := sq.Update("project__template").
		SetMap(fields).
		Where(sq.Eq{
			"id":         tmpl.ID,
			"project_id": tmpl.ProjectID,
		}).
		ToSql()
	if err != nil {
		return err
	}

	_, err = d.exec(query, args...)
	if err != nil {
		return err
	}

	err = d.UpdateTemplateVaults(tmpl.ProjectID, tmpl.ID, tmpl.Vaults)
	if err != nil {
		return err
	}

	tmpl.ApplyLegacyEnvironmentField()
	return d.UpdateTemplateEnvironments(tmpl.ProjectID, tmpl.ID, tmpl.EnvironmentIDs)
}

func (d *SqlDb) GetTemplateEnvironments(projectID int, templateID int) (environmentIDs []int, err error) {
	environmentIDs = make([]int, 0)

	var rows []struct {
		EnvironmentID int `db:"environment_id"`
	}

	_, err = d.selectAll(
		&rows,
		"select environment_id from project__template_environment "+
			"where project_id=? and template_id=? order by environment_id",
		projectID,
		templateID,
	)

	if err != nil {
		return
	}

	for _, r := range rows {
		environmentIDs = append(environmentIDs, r.EnvironmentID)
	}

	return
}

func (d *SqlDb) UpdateTemplateEnvironments(projectID int, templateID int, environmentIDs []int) (err error) {
	_, err = d.exec(
		"delete from project__template_environment where project_id=? and template_id=?",
		projectID,
		templateID,
	)
	if err != nil {
		return
	}

	seen := make(map[int]bool)
	for _, envID := range environmentIDs {
		if seen[envID] {
			continue
		}
		seen[envID] = true

		_, err = d.exec(
			"insert into project__template_environment (project_id, template_id, environment_id) values (?, ?, ?)",
			projectID,
			templateID,
			envID,
		)
		if err != nil {
			return
		}
	}

	return
}

func (d *SqlDb) SetTemplateDescription(projectID int, templateID int, description string) (err error) {

	_, err = d.exec("update project__template set "+
		"description=? "+
		"where id=? and project_id=?",
		description,
		templateID,
		projectID,
	)

	return
}

func (d *SqlDb) getTemplates(
	projectID int,
	userID *int,
	filter db.TemplateFilter,
	params db.RetrieveQueryParams,
	loadVaults bool,
) (templates []db.TemplateWithPerms, err error) {

	pp, err := params.Validate(db.TemplateProps)
	if err != nil {
		return
	}
	if err = filter.ValidateSearch(); err != nil {
		return
	}
	normalizedSearch := db.NormalizeTemplateSearch(filter.Search)

	templates = make([]db.TemplateWithPerms, 0)

	var view db.View

	if filter.ViewID != nil {
		view, err = d.GetView(projectID, *filter.ViewID)
		if err != nil {
			return
		}
	}

	fields := []string{
		"pt.id",
		"pt.project_id",
		"pt.inventory_id",
		"pt.repository_id",
		"pt.name",
		"pt.description",
		"pt.playbook",
		"pt.working_directory",
		"pt.arguments",
		"pt.allow_override_args_in_task",
		"pt.build_template_id",
		"pt.start_version",
		"pt.view_id",
		"pt.`app`",
		"pt.`git_branch`",
		"pt.survey_vars",
		"pt.`type`",
		"pt.`tasks`",
		"pt.runner_tag",
		"pt.task_params",
		"pt.allow_override_branch_in_task",
		"pt.allow_parallel_tasks",
		"pt.jwt_params",
		"pt.executor_image",
		"pt.suppress_success_alerts",
		"pt.suppress_error_alerts",
		"(SELECT `id` FROM `task` WHERE template_id = pt.id ORDER BY `id` DESC LIMIT 1) last_task_id",
	}

	// runner_tags was added after the original template schema. Keep reads from
	// pre-policy database versions compatible while including every current tag
	// in the literal search surface.
	hasRunnerTagPolicy, err := d.IsMigrationApplied(db.Migration{Version: "2.20.6"})
	if err != nil {
		return
	}
	if hasRunnerTagPolicy {
		fields = append(fields, "pt.runner_tags")
	}

	if userID != nil {
		fields = append(fields, "0 permissions")
	}

	q := sq.Select(fields...).From("project__template pt")

	if filter.App != nil {
		q = q.Where("pt.app=?", *filter.App)
	}

	if filter.ViewID != nil {
		switch view.Type {
		case db.ViewTypeCustom:
			q = q.Where("pt.view_id=?", *filter.ViewID)
		case db.ViewTypeAll:
			// TODO: implement filter
		}
	}

	if filter.BuildTemplateID != nil {
		q = q.Where("pt.build_template_id=?", *filter.BuildTemplateID)
		if filter.AutorunOnly {
			q = q.Where("pt.autorun=true")
		}
	}

	order := "ASC"
	var sortBy string

	if pp.SortBy != "" { // order by query param has priority
		sortBy = pp.SortBy
		if pp.SortInverted {
			order = "DESC"
		}
	} else if filter.ViewID != nil && view.SortColumn != nil {
		sortBy = *view.SortColumn
		if view.SortReverse {
			order = "DESC"
		}
	}

	switch sortBy {
	case "name", "playbook":
		q = q.Where("pt.project_id=?", projectID).
			OrderBy("pt."+sortBy+" "+order, "pt.id ASC")
	case "inventory":
		q = q.LeftJoin("project__inventory pi ON (pt.inventory_id = pi.id)").
			Where("pt.project_id=?", projectID).
			OrderBy("pi.name "+order, "pt.id ASC")
	case "repository":
		q = q.LeftJoin("project__repository pr ON (pt.repository_id = pr.id)").
			Where("pt.project_id=?", projectID).
			OrderBy("pr.name "+order, "pt.id ASC")
	default:
		q = q.Where("pt.project_id=?", projectID).
			OrderBy("pt.name "+order, "pt.id ASC")
	}

	query, args, err := q.ToSql()

	if err != nil {
		return
	}

	var tpls []templateWithLastTask

	_, err = d.selectAll(&tpls, query, args...)

	if err != nil {
		return
	}

	// Permission evaluation remains the sole authority for visibility. Search and
	// pagination happen only after that evaluator so a hidden template cannot
	// influence matches, offsets, or response lengths.
	visible := make([]templateWithLastTask, 0, len(tpls))
	for _, tpl := range tpls {
		template := tpl.TemplateWithPerms
		if userID != nil {
			var permissionContext db.TemplatePermissionContext
			permissionContext, err = d.GetTemplatePermissionContext(projectID, template.ID, *userID)
			if err != nil {
				return
			}
			if !permissionContext.EffectivePermissions.Can(db.CanReadTemplate) {
				continue
			}
			legacyPermissions := db.TemplatePermissionsToProject(permissionContext.EffectivePermissions)
			template.Permissions = &legacyPermissions
		}
		tpl.TemplateWithPerms = template
		visible = append(visible, tpl)
	}

	if normalizedSearch != "" && templateSearchCanUseSQLCandidateFilter(normalizedSearch) {
		visible, err = d.filterVisibleTemplateSearchCandidates(
			q, visible, normalizedSearch, hasRunnerTagPolicy, userID != nil,
		)
		if err != nil {
			return
		}
	}

	// The SQL candidate filter is only an optimization for ASCII terms. Go
	// matching remains authoritative for Unicode case folding and control
	// characters, whose LIKE behavior differs across supported SQL engines.
	matched := visible[:0]
	for _, tpl := range visible {
		if tpl.Template.MatchesSearch(normalizedSearch) {
			matched = append(matched, tpl)
		}
	}
	visible = matched

	if pp.Offset >= len(visible) {
		visible = visible[:0]
	} else if pp.Offset > 0 {
		visible = visible[pp.Offset:]
	}
	if pp.Count > 0 && pp.Count < len(visible) {
		visible = visible[:pp.Count]
	}

	taskIDs := make([]int, 0, len(visible))
	for _, tpl := range visible {
		if tpl.LastTaskID != nil {
			taskIDs = append(taskIDs, *tpl.LastTaskID)
		}
	}
	var tasks []db.TaskWithTpl
	if len(taskIDs) > 0 {
		err = d.getTasks(projectID, nil, nil, taskIDs, db.RetrieveQueryParams{}, &tasks)
		if err != nil {
			return
		}
	}

	for _, tpl := range visible {
		template := tpl.TemplateWithPerms
		if tpl.LastTaskID != nil {
			for _, tsk := range tasks {
				if tsk.ID == *tpl.LastTaskID {
					template.LastTask = &tsk
					break
				}
			}
		}
		if tpl.SurveyVarsJSON != nil {
			if err2 := json.Unmarshal([]byte(*tpl.SurveyVarsJSON), &template.SurveyVars); err2 != nil {
				log.WithFields(log.Fields{
					"context":     common_errors.GetErrorContext(),
					"project_id":  projectID,
					"template_id": template.ID,
					"hint":        "validate JSON array in project__template.survey_vars",
				}).Error("failed to unmarshal template survey vars")
			}
		}
		if loadVaults {
			template.Vaults, err = d.GetTemplateVaults(projectID, template.ID)
			if err != nil {
				return
			}
		}
		template.EnvironmentIDs, err = d.GetTemplateEnvironments(projectID, template.ID)
		if err != nil {
			return
		}
		if len(template.EnvironmentIDs) > 0 {
			template.EnvironmentID = template.EnvironmentIDs[0]
		}
		templates = append(templates, template)
	}

	return
}

func (d *SqlDb) GetTemplatesWithPermissions(projectID int, userID int, filter db.TemplateFilter, params db.RetrieveQueryParams) (templates []db.TemplateWithPerms, err error) {
	return d.getTemplates(projectID, &userID, filter, params, false)
}

func (d *SqlDb) GetTemplates(projectID int, filter db.TemplateFilter, params db.RetrieveQueryParams) (templates []db.Template, err error) {
	res, err := d.getTemplates(projectID, nil, filter, params, true)
	if err != nil {
		return
	}

	templates = make([]db.Template, 0, len(res))

	for _, tpl := range res {
		templates = append(templates, tpl.Template)
	}

	return
}

// GetTemplateByName returns the template of the project with the given name.
// Template names are not unique per project, so an ambiguous name is rejected
// instead of silently running one of the matching templates.
func (d *SqlDb) GetTemplateByName(projectID int, name string) (template db.Template, err error) {
	var templates []db.Template

	_, err = d.selectAll(
		&templates,
		"select * from project__template where project_id=? and name=? limit 2",
		projectID,
		name)

	if err != nil {
		return
	}

	switch len(templates) {
	case 0:
		err = db.ErrNotFound
		return
	case 1:
	default:
		err = common_errors.NewValidationError("more than one template is named " + name + ", use template_id")
		return
	}

	template = templates[0]
	err = db.FillTemplate(d, &template)
	return
}

func (d *SqlDb) GetTemplate(projectID int, templateID int) (template db.Template, err error) {
	err = d.selectOne(
		&template,
		"select * from project__template where project_id=? and id=?",
		projectID,
		templateID)

	if err != nil {
		return
	}

	err = db.FillTemplate(d, &template)
	return
}

func (d *SqlDb) DeleteTemplate(projectID int, templateID int) error {
	_, err := d.exec("delete from project__template where project_id=? and id=?", projectID, templateID)
	return err
}

func (d *SqlDb) GetTemplateRefs(projectID int, templateID int) (db.ObjectReferrers, error) {
	return d.getObjectRefs(projectID, db.TemplateProps, templateID)
}

func (d *SqlDb) GetTemplateRole(projectID int, templateID int, id int) (templateRole db.TemplateRolePerm, err error) {

	query, args, err := sq.Select("*").
		From("project__template_role").
		Where("project_id = ?", projectID).
		Where("template_id = ?", templateID).
		Where("id = ?", id).
		ToSql()

	if err != nil {
		return
	}

	err = d.selectOne(&templateRole, query, args...)

	return
}

func (d *SqlDb) GetTemplatePermission(projectID int, templateID int, userID int) (perm db.ProjectUserPermission, err error) {
	context, err := d.GetTemplatePermissionContext(projectID, templateID, userID)
	if err != nil {
		return 0, err
	}
	return db.TemplatePermissionsToProject(context.EffectivePermissions), nil
}

func (d *SqlDb) GetTemplateRoles(projectID int, templateID int) (roles []db.TemplateRolePerm, err error) {
	query, args, err := sq.Select("*").
		From("project__template_role").
		Where("project_id = ?", projectID).
		Where("template_id = ?", templateID).
		ToSql()

	if err != nil {
		return
	}

	_, err = d.selectAll(&roles, query, args...)
	return
}
func (d *SqlDb) CreateTemplateRole(role db.TemplateRolePerm) (newRole db.TemplateRolePerm, err error) {
	role, err = d.normalizeTemplateRole(role)
	if err != nil {
		return db.TemplateRolePerm{}, err
	}
	insertID, err := d.insert(
		"id",
		"insert into project__template_role "+
			"(project_id, template_id, role_slug, role_id, permissions, allowed_permissions, denied_permissions, revision) "+
			"values (?, ?, ?, ?, ?, ?, ?, ?)",
		role.ProjectID,
		role.TemplateID,
		role.RoleSlug,
		role.RoleID,
		role.Permissions,
		role.AllowedPermissions,
		role.DeniedPermissions,
		role.Revision)

	if err != nil {
		return
	}

	newRole = role
	newRole.ID = insertID
	return
}
func (d *SqlDb) DeleteTemplateRole(
	projectID int,
	templateID int,
	id int,
	expectedRevision int,
) error {
	if expectedRevision <= 0 {
		return db.ErrTemplateRoleRevisionConflict
	}
	result, err := d.exec(
		"delete from project__template_role where project_id=? and template_id=? and id=? and revision=?",
		projectID, templateID, id, expectedRevision,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		if _, findErr := d.GetTemplateRole(projectID, templateID, id); errors.Is(findErr, db.ErrNotFound) {
			return db.ErrNotFound
		} else if findErr != nil {
			return findErr
		}
		return db.ErrTemplateRoleRevisionConflict
	}
	return nil
}
func (d *SqlDb) UpdateTemplateRole(
	role db.TemplateRolePerm,
	expectedRevision int,
) (db.TemplateRolePerm, error) {
	if expectedRevision <= 0 || role.Revision != expectedRevision {
		return db.TemplateRolePerm{}, db.ErrTemplateRoleRevisionConflict
	}
	role, err := d.normalizeTemplateRole(role)
	if err != nil {
		return db.TemplateRolePerm{}, err
	}
	result, err := d.exec(
		"update project__template_role set role_slug=?, role_id=?, permissions=?, "+
			"allowed_permissions=?, denied_permissions=?, revision=revision+1 "+
			"where project_id=? and template_id=? and id=? and revision=?",
		role.RoleSlug,
		role.RoleID,
		role.Permissions,
		role.AllowedPermissions,
		role.DeniedPermissions,
		role.ProjectID,
		role.TemplateID,
		role.ID,
		expectedRevision,
	)
	if err != nil {
		return db.TemplateRolePerm{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return db.TemplateRolePerm{}, err
	}
	if rows != 1 {
		if _, findErr := d.GetTemplateRole(role.ProjectID, role.TemplateID, role.ID); errors.Is(findErr, db.ErrNotFound) {
			return db.TemplateRolePerm{}, db.ErrNotFound
		} else if findErr != nil {
			return db.TemplateRolePerm{}, findErr
		}
		return db.TemplateRolePerm{}, db.ErrTemplateRoleRevisionConflict
	}
	return d.GetTemplateRole(role.ProjectID, role.TemplateID, role.ID)
}
