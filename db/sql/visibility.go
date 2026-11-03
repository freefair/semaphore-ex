package sql

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/db"
)

var readBit = strconv.Itoa(int(db.CanReadProject))

// memberRoles lists the roles of a member, assigned directly or through a group mapping.
func memberRoles(projectID int, userID int) (string, []any) {
	return `select pur.role_id from project__user_role pur where pur.project_id = ? and pur.user_id = ?`, []any{projectID, userID}
}

func readsProject(projectID int, userID int) (string, []any) {
	_, args := memberRoles(projectID, userID)
	return "exists (select 1 from project__user_role pur left join `role` r on r.role_id=pur.role_id where pur.project_id=? and pur.user_id=? and (pur.role_id in ('builtin:owner','builtin:manager','builtin:task_runner','builtin:guest') or r.permissions & " + readBit + " <> 0))", args
}

func readGrant(table string, on string, projectID int, userID int) (string, []any) {
	roles, args := memberRoles(projectID, userID)
	return "exists (select 1 from " + table + " g where " + on + " and g.permissions & " + readBit + " <> 0 and g.role_id in (" + roles + "))", args
}

// VisibleWorkflowsWhere is a coarse SQL filter. EX workflow access policies
// must still be evaluated for every result before it is returned.
func VisibleWorkflowsWhere(projectID int, userID int, alias string) (string, []any) {
	project := "exists (select 1 from project__user_role pur left join `role` r on r.role_id=pur.role_id where pur.project_id=? and pur.user_id=? and (pur.role_id in ('builtin:owner','builtin:manager','builtin:task_runner','builtin:guest') or r.permissions & " + strconv.Itoa(int(db.CanViewWorkflows)) + " <> 0))"
	args := []any{projectID, userID}
	grant, grantArgs := readGrant("project__workflow_role", "g.workflow_id = "+alias+".id", projectID, userID)
	return "(" + project + " or " + grant + ")", append(args, grantArgs...)
}

func visibleTemplatesWhere(projectID int, userID int, alias string) (string, []any) {
	// This is only a candidate filter: the EX evaluator applies inherited
	// permissions and all matching allow/deny overrides before returning rows.
	project, args := readsProject(projectID, userID)
	resourceView := "exists (select 1 from project__user_role pur join `role` r on r.role_id=pur.role_id where pur.project_id=? and pur.user_id=? and r.permissions & " + strconv.Itoa(int(db.CanViewProjectResources)) + " <> 0)"
	grant := "exists (select 1 from project__template_role g where g.template_id=" + alias + ".id and g.allowed_permissions & 1 <> 0)"
	args = append(args, projectID, userID)
	return "(" + project + " or " + resourceView + " or " + grant + ")", args
}

// exactVisibleTasksWhere evaluates current template overrides and workflow
// snapshot policy before SQL pagination or aggregation.
func (d *SqlDb) exactVisibleTasksWhere(projectID, userID int) (string, []any, error) {
	var pairs []struct {
		TemplateID    int  `db:"template_id"`
		WorkflowRunID *int `db:"workflow_run_id"`
	}
	if _, err := d.selectAll(&pairs, "select distinct template_id, workflow_run_id from task where project_id=?", projectID); err != nil {
		return "", nil, err
	}
	templates := map[int]bool{}
	runs := map[int]bool{}
	for _, pair := range pairs {
		if pair.WorkflowRunID == nil {
			templates[pair.TemplateID] = false
		} else {
			runs[*pair.WorkflowRunID] = false
		}
	}
	for id := range templates {
		visible, err := d.userCanViewTask(projectID, id, nil, userID)
		if err != nil {
			return "", nil, err
		}
		templates[id] = visible
	}
	for id := range runs {
		visible, err := d.userCanViewTask(projectID, 0, &id, userID)
		if err != nil {
			return "", nil, err
		}
		runs[id] = visible
	}
	templateIDs := []string{}
	runIDs := []string{}
	for id, visible := range templates {
		if id > 0 && visible {
			templateIDs = append(templateIDs, strconv.Itoa(id))
		}
	}
	for id, visible := range runs {
		if id > 0 && visible {
			runIDs = append(runIDs, strconv.Itoa(id))
		}
	}
	conditions := []string{}
	if len(templateIDs) > 0 {
		conditions = append(conditions, "(task.workflow_run_id is null and task.template_id in ("+strings.Join(templateIDs, ",")+"))")
	}
	if len(runIDs) > 0 {
		conditions = append(conditions, "task.workflow_run_id in ("+strings.Join(runIDs, ",")+")")
	}
	if len(conditions) == 0 {
		return "1=0", nil, nil
	}
	return "(" + strings.Join(conditions, " or ") + ")", nil, nil
}

func (d *SqlDb) userCanViewTask(projectID, templateID int, workflowRunID *int, userID int) (bool, error) {
	if workflowRunID == nil {
		count, err := d.Sql().SelectInt(d.PrepareQuery("select count(*) from project__template where project_id=? and id=?"), projectID, templateID)
		if err != nil {
			return false, err
		}
		if count == 0 {
			return false, nil
		}
		template, err := d.GetTemplatePermissionContext(projectID, templateID, userID)
		if err != nil {
			return false, err
		}
		return template.EffectivePermissions.Can(db.CanReadTemplate), nil
	}
	var err error
	var run struct {
		Snapshot   string `db:"definition_snapshot"`
		WorkflowID int    `db:"workflow_template_id"`
	}
	if err = d.Sql().SelectOne(&run, d.PrepareQuery("select definition_snapshot,workflow_template_id from project__workflow_run where project_id=? and id=?"), projectID, *workflowRunID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	var workflow db.WorkflowTemplate
	if err = json.Unmarshal([]byte(run.Snapshot), &workflow); err != nil {
		return false, nil
	}
	if workflow.ProjectID != projectID || workflow.ID <= 0 || workflow.ID != run.WorkflowID {
		return false, nil
	}
	count, err := d.Sql().SelectInt(d.PrepareQuery("select count(*) from project__workflow_template where project_id=? and id=?"), projectID, workflow.ID)
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	state, err := pro_interfaces.ResolveWorkflowObjectAuthorizationState(d, projectID, workflow.ID, userID)
	if errors.Is(err, db.ErrProjectWorkflowRoleIdentityUnavailable) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return pro_interfaces.AuthorizeWorkflowReadForState(workflow, state).Allowed, nil
}

func filtered(viewer *db.Viewer) bool { return viewer != nil && !viewer.Admin }

// taskVisible says whether the member may see the task, a missing id counts as visible.
func (d *SqlDb) taskVisible(projectID int, taskID *int, userID int) (bool, error) {
	if taskID == nil {
		return true, nil
	}
	var task struct {
		TemplateID    int  `db:"template_id"`
		WorkflowRunID *int `db:"workflow_run_id"`
	}
	if err := d.Sql().SelectOne(&task, d.PrepareQuery("select template_id,workflow_run_id from task where project_id=? and id=?"), projectID, *taskID); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return d.userCanViewTask(projectID, task.TemplateID, task.WorkflowRunID, userID)
}

// GetTaskViewers lists who may see a task: members with read_project, members whose roles hold a read grant
// on the template or on the workflow of the run, and every admin.
func (d *SqlDb) GetTaskViewers(projectID int, templateID int, workflowRunID *int) ([]int, error) {
	return d.batchTaskViewers(projectID, templateID, workflowRunID)
}
