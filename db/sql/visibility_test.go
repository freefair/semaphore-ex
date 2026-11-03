package sql

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// visibilityFixture: templates A and B, workflow "release" with a run whose node task uses template B, and four
// members: reader (read_project), grantee (sees A through a grant), runGrantee (sees "release" through a grant)
// and nobody.
type visibilityFixture struct {
	store      *SqlDb
	projectID  int
	a, b       db.Template
	workflowID int
	taskA      db.Task
	taskB      db.Task
	nodeTask   db.Task
	reader     db.User
	grantee    db.User
	runGrantee db.User
	nobody     db.User
}

func newVisibilityFixture(t *testing.T) visibilityFixture {
	t.Helper()
	store := InitConfigCreateTestStore()
	projectID, repositoryID := newTemplateTestProject(t, store)
	f := visibilityFixture{store: store, projectID: projectID}
	var err error
	f.a, err = store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryID, Name: "a", Playbook: "a.yml"})
	require.NoError(t, err)
	f.b, err = store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryID, Name: "b", Playbook: "b.yml"})
	require.NoError(t, err)
	f.workflowID = insertWorkflow(t, store, projectID, "release")
	_, err = store.exec("insert into project__workflow_run (project_id, workflow_template_id, status, definition_snapshot) values (?, ?, ?, ?)", projectID, f.workflowID, "running", fmt.Sprintf(`{"id":%d,"project_id":%d}`, f.workflowID, projectID))
	require.NoError(t, err)
	runID, err := store.Sql().SelectInt(store.PrepareQuery("select id from project__workflow_run where project_id = ?"), projectID)
	require.NoError(t, err)
	run := int(runID)
	f.taskA, err = store.CreateTask(db.Task{ProjectID: projectID, TemplateID: f.a.ID, Created: time.Now()}, 0)
	require.NoError(t, err)
	f.taskB, err = store.CreateTask(db.Task{ProjectID: projectID, TemplateID: f.b.ID, Created: time.Now()}, 0)
	require.NoError(t, err)
	f.nodeTask, err = store.CreateTask(db.Task{ProjectID: projectID, TemplateID: f.b.ID, WorkflowRunID: &run, Created: time.Now()}, 0)
	require.NoError(t, err)

	member := func(name string, mask db.ProjectUserPermission) (db.User, db.Role) {
		role, err := store.CreateRole(db.Role{Slug: name, Name: name, Permissions: mask, ProjectID: &projectID})
		require.NoError(t, err)
		user, err := store.CreateUserWithoutPassword(db.User{Username: name, Name: name, Email: name + "@example.com"})
		require.NoError(t, err)
		_, err = store.CreateProjectUser(db.ProjectUser{ProjectID: projectID, UserID: user.ID, RoleID: &role.ID, Revision: 1})
		require.NoError(t, err)
		return user, role
	}
	f.reader, _ = member("reader", db.CanReadProject|db.CanViewProjectResources|db.CanViewWorkflows)
	f.nobody, _ = member("nobody", 0)
	var role db.Role
	f.grantee, role = member("grantee", 0)
	_, err = store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: projectID, TemplateID: f.a.ID, RoleID: &role.ID, AllowedPermissions: db.CanReadTemplate, Revision: 1})
	require.NoError(t, err)
	f.runGrantee, role = member("run-grantee", 0)
	_, err = store.CreateWorkflowRole(db.WorkflowRolePerm{ProjectID: projectID, WorkflowID: f.workflowID, RoleID: role.ID, Permissions: db.CanReadProject | db.CanRunWorkflows})
	require.NoError(t, err)
	return f
}

func templateNames(templates []db.TemplateWithPerms) []string {
	names := []string{}
	for _, template := range templates {
		names = append(names, template.Name)
	}
	return names
}

func taskIDs(tasks []db.TaskWithTpl) []int {
	ids := []int{}
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	return ids
}

func TestVisibility_Templates(t *testing.T) {
	f := newVisibilityFixture(t)
	tests := []struct {
		name   string
		viewer *db.Viewer
		want   []string
	}{
		{"reader sees the project", &db.Viewer{UserID: f.reader.ID}, []string{"a", "b"}},
		{"grantee sees the granted template", &db.Viewer{UserID: f.grantee.ID}, []string{"a"}},
		{"a workflow grant shows no template", &db.Viewer{UserID: f.runGrantee.ID}, []string{}},
		{"nobody sees nothing", &db.Viewer{UserID: f.nobody.ID}, []string{}},
		{"admin is not filtered", &db.Viewer{UserID: f.nobody.ID, Admin: true}, []string{"a", "b"}},
		{"no viewer, no filter", nil, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viewerID := f.reader.ID
			if tt.viewer != nil && !tt.viewer.Admin {
				viewerID = tt.viewer.UserID
			}
			templates, err := f.store.GetTemplatesWithPermissions(f.projectID, viewerID, db.TemplateFilter{Viewer: tt.viewer}, db.RetrieveQueryParams{})
			require.NoError(t, err)
			assert.Equal(t, tt.want, templateNames(templates))
		})
	}
}

func TestVisibility_Tasks(t *testing.T) {
	f := newVisibilityFixture(t)
	tests := []struct {
		name   string
		viewer *db.Viewer
		want   []int
	}{
		{"reader sees every task", &db.Viewer{UserID: f.reader.ID}, []int{f.nodeTask.ID, f.taskB.ID, f.taskA.ID}},
		{"grantee sees the tasks of the granted template", &db.Viewer{UserID: f.grantee.ID}, []int{f.taskA.ID}},
		{"workflow grant shows its run tasks", &db.Viewer{UserID: f.runGrantee.ID}, []int{f.nodeTask.ID}},
		{"nobody sees nothing", &db.Viewer{UserID: f.nobody.ID}, []int{}},
		{"admin is not filtered", &db.Viewer{UserID: f.nobody.ID, Admin: true}, []int{f.nodeTask.ID, f.taskB.ID, f.taskA.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := db.RetrieveQueryParams{TaskFilter: &db.TaskFilter{Viewer: tt.viewer}}
			tasks, err := f.store.GetProjectTasks(f.projectID, params)
			require.NoError(t, err)
			assert.Equal(t, tt.want, taskIDs(tasks))

			stats, err := f.store.GetTaskStats(f.projectID, nil, db.TaskStatUnitDay, db.TaskFilter{Viewer: tt.viewer})
			require.NoError(t, err)
			count := 0
			for _, stat := range stats {
				for _, n := range stat.CountByStatus {
					count += n
				}
			}
			assert.Equal(t, len(tt.want), count, "stats count only the visible tasks")
		})
	}
}

// The people a task's live updates go to: whoever may see the task, plus the admins.
func TestVisibility_TaskViewers(t *testing.T) {
	f := newVisibilityFixture(t)
	root, err := f.store.CreateUserWithoutPassword(db.User{Username: "root", Name: "root", Email: "root@example.com", Admin: true})
	require.NoError(t, err)

	viewers, err := f.store.GetTaskViewers(f.projectID, f.taskA.TemplateID, nil)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{f.reader.ID, f.grantee.ID, root.ID}, viewers)

	viewers, err = f.store.GetTaskViewers(f.projectID, f.nodeTask.TemplateID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{f.reader.ID, f.runGrantee.ID, root.ID}, viewers)
}

// A deploy task carries its build task only when the member may see that task too.
func TestVisibility_BuildTaskOfHiddenTemplate(t *testing.T) {
	f := newVisibilityFixture(t)
	deploy, err := f.store.CreateTask(db.Task{ProjectID: f.projectID, TemplateID: f.a.ID, BuildTaskID: &f.taskB.ID, Created: time.Now()}, 0)
	require.NoError(t, err)

	first := func(viewer *db.Viewer) db.TaskWithTpl {
		tasks, err := f.store.GetProjectTasks(f.projectID, db.RetrieveQueryParams{TaskFilter: &db.TaskFilter{Viewer: viewer}})
		require.NoError(t, err)
		require.NotEmpty(t, tasks)
		require.Equal(t, deploy.ID, tasks[0].ID)
		return tasks[0]
	}
	assert.NotNil(t, first(&db.Viewer{UserID: f.reader.ID}).BuildTask, "the reader sees the build task")
	assert.Nil(t, first(&db.Viewer{UserID: f.grantee.ID}).BuildTask, "the grantee may not read template b")
}

func TestVisibility_MoreThanThousandWorkflowRuns(t *testing.T) {
	f := newVisibilityFixture(t)
	snapshot := fmt.Sprintf(`{"id":%d,"project_id":%d}`, f.workflowID, f.projectID)
	for i := 0; i < 1001; i++ {
		result, err := f.store.exec("insert into project__workflow_run (project_id,workflow_template_id,status,definition_snapshot,correlation_id) values (?,?,?,?,?)", f.projectID, f.workflowID, "running", snapshot, fmt.Sprintf("visibility-%d", i))
		require.NoError(t, err)
		id, err := result.LastInsertId()
		require.NoError(t, err)
		runID := int(id)
		_, err = f.store.CreateTask(db.Task{ProjectID: f.projectID, TemplateID: f.b.ID, WorkflowRunID: &runID, Created: time.Now()}, 0)
		require.NoError(t, err)
	}
	where, args, err := f.store.exactVisibleTasksWhere(f.projectID, f.reader.ID)
	require.NoError(t, err)
	count, err := f.store.Sql().SelectInt(f.store.PrepareQuery("select count(*) from task where task.project_id=? and "+where), append([]any{f.projectID}, args...)...)
	require.NoError(t, err)
	assert.EqualValues(t, 1004, count)
}

func TestBatchTaskViewersMatchCurrentPerUserPermissions(t *testing.T) {
	f := newVisibilityFixture(t)
	builtin, err := f.store.CreateUserWithoutPassword(db.User{Username: "batch-builtin", Name: "Batch builtin", Email: "batch-builtin@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.projectID, UserID: builtin.ID, Role: db.ProjectGuest, Revision: 1})
	require.NoError(t, err)
	users := []db.User{f.reader, f.grantee, f.runGrantee, f.nobody, builtin}
	for _, test := range []struct {
		name     string
		template int
		run      *int
	}{
		{"template a", f.a.ID, nil}, {"template b", f.b.ID, nil}, {"workflow", f.b.ID, f.nodeTask.WorkflowRunID},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected := []int{}
			for _, user := range users {
				allowed, err := f.store.userCanViewTask(f.projectID, test.template, test.run, user.ID)
				require.NoError(t, err)
				if allowed {
					expected = append(expected, user.ID)
				}
			}
			actual, err := f.store.GetTaskViewers(f.projectID, test.template, test.run)
			require.NoError(t, err)
			assert.ElementsMatch(t, expected, actual)
		})
	}
}

func TestMalformedWorkflowSnapshotHidesOnlyItsTasks(t *testing.T) {
	f := newVisibilityFixture(t)
	_, err := f.store.exec("update project__workflow_run set definition_snapshot=? where id=?", "{", *f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	tasks, err := f.store.GetProjectTasks(f.projectID, db.RetrieveQueryParams{TaskFilter: &db.TaskFilter{Viewer: &db.Viewer{UserID: f.reader.ID}}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{f.taskA.ID, f.taskB.ID}, taskIDs(tasks))
	viewers, err := f.store.GetTaskViewers(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.Empty(t, viewers)
}

func TestBatchTaskViewersHonorsDenyAndWorkflowPolicy(t *testing.T) {
	f := newVisibilityFixture(t)
	readerRole, err := f.store.GetProjectRole(f.projectID, "reader")
	require.NoError(t, err)
	deny, err := f.store.CreateTemplateRole(db.TemplateRolePerm{ProjectID: f.projectID, TemplateID: f.a.ID, RoleID: &readerRole.ID, DeniedPermissions: db.CanReadTemplate})
	require.NoError(t, err)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.a.ID, nil)
	require.NoError(t, err)
	assert.NotContains(t, viewers, f.reader.ID)
	assert.Contains(t, viewers, f.grantee.ID)
	require.NoError(t, f.store.DeleteTemplateRole(f.projectID, f.a.ID, deny.ID, deny.Revision))
	viewers, err = f.store.GetTaskViewers(f.projectID, f.a.ID, nil)
	require.NoError(t, err)
	assert.Contains(t, viewers, f.reader.ID)
	snapshot, err := json.Marshal(db.WorkflowTemplate{ID: f.workflowID, ProjectID: f.projectID, AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{db.BuiltinProjectRoleReferenceManager}}})
	require.NoError(t, err)
	_, err = f.store.exec("update project__workflow_run set definition_snapshot=? where id=?", string(snapshot), *f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	viewers, err = f.store.GetTaskViewers(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.NotContains(t, viewers, f.reader.ID)
	assert.NotContains(t, viewers, f.runGrantee.ID)
}

func TestBatchWorkflowViewersRejectStaleManagedRoles(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*testing.T, *SqlDb, db.Project, db.User, db.Role, time.Time)
		table string
	}{
		{"ldap", setupLDAPWorkflowRoleIdentity, "ldap_group_mapping"},
		{"oidc", setupOIDCWorkflowRoleIdentity, "oidc_group_mapping"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			project, user := workflowRoleIdentityProjectUser(t, store, "batch-"+tt.name)
			role, err := store.CreateProjectRole(db.Role{ID: db.ProjectRoleID("role_batch_" + tt.name), Slug: "role_batch_" + tt.name, Name: "Workflow viewer", ProjectID: &project.ID, Permissions: db.CanViewWorkflows, Revision: 1})
			require.NoError(t, err)
			tt.setup(t, store, project, user, role, time.Unix(1900000000, 0).UTC())
			workflowID := insertWorkflow(t, store, project.ID, "directory workflow")
			snapshot, err := json.Marshal(db.WorkflowTemplate{ID: workflowID, ProjectID: project.ID})
			require.NoError(t, err)
			result, err := store.exec("insert into project__workflow_run (project_id,workflow_template_id,status,definition_snapshot) values (?,?,?,?)", project.ID, workflowID, "running", string(snapshot))
			require.NoError(t, err)
			id, err := result.LastInsertId()
			require.NoError(t, err)
			runID := int(id)
			viewers, err := store.GetTaskViewers(project.ID, 0, &runID)
			require.NoError(t, err)
			assert.Contains(t, viewers, user.ID)
			_, err = store.exec("update "+tt.table+" set enabled=false where id=?", "workflow-approvers")
			require.NoError(t, err)
			viewers, err = store.GetTaskViewers(project.ID, 0, &runID)
			require.NoError(t, err)
			assert.NotContains(t, viewers, user.ID)
		})
	}
}

func TestMissingHistoricalTaskReferencesFailClosed(t *testing.T) {
	f := newVisibilityFixture(t)
	missing := 12345678
	visible, err := f.store.userCanViewTask(f.projectID, missing, nil, f.reader.ID)
	require.NoError(t, err)
	assert.False(t, visible)
	visible, err = f.store.userCanViewTask(f.projectID, 0, &missing, f.reader.ID)
	require.NoError(t, err)
	assert.False(t, visible)
	viewers, err := f.store.GetTaskViewers(f.projectID, 0, &missing)
	require.NoError(t, err)
	assert.Empty(t, viewers)
	snapshot, err := json.Marshal(db.WorkflowTemplate{ID: f.workflowID + 100, ProjectID: f.projectID})
	require.NoError(t, err)
	_, err = f.store.exec("update project__workflow_run set definition_snapshot=? where id=?", string(snapshot), *f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	visible, err = f.store.userCanViewTask(f.projectID, 0, f.nodeTask.WorkflowRunID, f.reader.ID)
	require.NoError(t, err)
	assert.False(t, visible)
}

func TestBatchWorkflowViewersMatchGlobalGrantAndPolicy(t *testing.T) {
	f := newVisibilityFixture(t)
	local, err := f.store.GetProjectRole(f.projectID, "nobody")
	require.NoError(t, err)
	global, err := f.store.CreateGlobalRole(db.Role{ID: "global-workflow-view", Slug: "global-workflow-view", Name: "Global workflow view", Revision: 1})
	require.NoError(t, err)
	member, err := f.store.GetProjectUser(f.projectID, f.nobody.ID)
	require.NoError(t, err)
	require.NoError(t, f.store.SetProjectUserRoleRefs(f.projectID, f.nobody.ID, []db.ProjectRoleID{local.ID, global.ID}, member.Revision))
	_, err = f.store.CreateWorkflowRole(db.WorkflowRolePerm{ProjectID: f.projectID, WorkflowID: f.workflowID, RoleID: global.ID, Permissions: db.CanReadProject})
	require.NoError(t, err)
	allowed, err := f.store.userCanViewTask(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID, f.nobody.ID)
	require.NoError(t, err)
	require.True(t, allowed)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.Contains(t, viewers, f.nobody.ID)
	snapshot, err := json.Marshal(db.WorkflowTemplate{ID: f.workflowID, ProjectID: f.projectID, AccessPolicy: db.WorkflowAccessPolicy{ViewRoleIDs: []db.ProjectRoleReference{db.ProjectRoleReferenceForCustomRole(global.ID)}}})
	require.NoError(t, err)
	_, err = f.store.exec("update project__workflow_run set definition_snapshot=? where id=?", string(snapshot), *f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	allowed, err = f.store.userCanViewTask(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID, f.nobody.ID)
	require.NoError(t, err)
	assert.False(t, allowed)
	viewers, err = f.store.GetTaskViewers(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.NotContains(t, viewers, f.nobody.ID)
}

func TestBatchWorkflowViewersFailClosedForGlobalOnlyAndDeletedRoles(t *testing.T) {
	f := newVisibilityFixture(t)
	global, err := f.store.CreateGlobalRole(db.Role{ID: "global-only-workflow", Slug: "global-only-workflow", Name: "Global only", Revision: 1})
	require.NoError(t, err)
	member, err := f.store.GetProjectUser(f.projectID, f.nobody.ID)
	require.NoError(t, err)
	require.NoError(t, f.store.SetProjectUserRoleRefs(f.projectID, f.nobody.ID, []db.ProjectRoleID{global.ID}, member.Revision))
	_, err = f.store.CreateWorkflowRole(db.WorkflowRolePerm{ProjectID: f.projectID, WorkflowID: f.workflowID, RoleID: global.ID, Permissions: db.CanReadProject})
	require.NoError(t, err)
	visible, err := f.store.userCanViewTask(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID, f.nobody.ID)
	require.NoError(t, err)
	assert.False(t, visible)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.NotContains(t, viewers, f.nobody.ID)
	role, err := f.store.GetProjectRole(f.projectID, "run-grantee")
	require.NoError(t, err)
	_, err = f.store.exec("delete from role where role_id=?", role.ID)
	require.NoError(t, err)
	visible, err = f.store.userCanViewTask(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID, f.runGrantee.ID)
	require.NoError(t, err)
	assert.False(t, visible)
	viewers, err = f.store.GetTaskViewers(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID)
	require.NoError(t, err)
	assert.NotContains(t, viewers, f.runGrantee.ID)
	assert.Contains(t, viewers, f.reader.ID, "a malformed member does not affect another member")
}

func TestTemplateViewerBatchMatchesLegacyRoleRevision(t *testing.T) {
	f := newVisibilityFixture(t)
	role, err := f.store.GetProjectRole(f.projectID, "reader")
	require.NoError(t, err)
	_, err = f.store.exec("update role set revision=0 where role_id=?", role.ID)
	require.NoError(t, err)
	visible, err := f.store.userCanViewTask(f.projectID, f.a.ID, nil, f.reader.ID)
	require.NoError(t, err)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.a.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, visible, containsViewerID(viewers, f.reader.ID))
}

func TestGlobalOnlyMemberRetainsVisibleNormalTasks(t *testing.T) {
	f := newVisibilityFixture(t)
	global, err := f.store.CreateGlobalRole(db.Role{ID: "overlay-global", Slug: "overlay-global", Name: "Overlay global", Permissions: db.CanViewProjectResources, Revision: 1})
	require.NoError(t, err)
	member, err := f.store.GetProjectUser(f.projectID, f.nobody.ID)
	require.NoError(t, err)
	require.NoError(t, f.store.SetProjectUserRoleRefs(f.projectID, f.nobody.ID, []db.ProjectRoleID{global.ID}, member.Revision))
	tasks, err := f.store.GetProjectTasks(f.projectID, db.RetrieveQueryParams{TaskFilter: &db.TaskFilter{Viewer: &db.Viewer{UserID: f.nobody.ID}}})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{f.taskA.ID, f.taskB.ID}, taskIDs(tasks))
}

func TestTemplateViewerBatchMatchesGlobalRole(t *testing.T) {
	f := newVisibilityFixture(t)
	global, err := f.store.CreateGlobalRole(db.Role{ID: "overlay-template-global", Slug: "overlay-template-global", Name: "Overlay global", Permissions: db.CanViewProjectResources, Revision: 1})
	require.NoError(t, err)
	member, err := f.store.GetProjectUser(f.projectID, f.nobody.ID)
	require.NoError(t, err)
	require.NoError(t, f.store.SetProjectUserRoleRefs(f.projectID, f.nobody.ID, []db.ProjectRoleID{global.ID}, member.Revision))
	visible, err := f.store.userCanViewTask(f.projectID, f.a.ID, nil, f.nobody.ID)
	require.NoError(t, err)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.a.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, visible, containsViewerID(viewers, f.nobody.ID))
}

func TestTemplateViewerBatchRejectsDeletedAssignedRole(t *testing.T) {
	f := newVisibilityFixture(t)
	user, err := f.store.CreateUserWithoutPassword(db.User{Username: "overlay-deleted", Name: "Overlay", Email: "overlay-deleted@example.invalid"})
	require.NoError(t, err)
	role, err := f.store.CreateProjectRole(db.Role{ID: "overlay-deleted-role", Slug: "overlay-deleted-role", Name: "Overlay deleted", ProjectID: &f.projectID, Revision: 1})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUserWithRoleRefs(db.ProjectUser{ProjectID: f.projectID, UserID: user.ID, Revision: 1}, []db.ProjectRoleID{role.ID, "builtin:guest"})
	require.NoError(t, err)
	_, err = f.store.exec("delete from role where role_id=?", role.ID)
	require.NoError(t, err)
	_, err = f.store.userCanViewTask(f.projectID, f.a.ID, nil, user.ID)
	assert.Error(t, err)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.a.ID, nil)
	require.NoError(t, err)
	assert.NotContains(t, viewers, user.ID)
}

func TestTemplateViewerBatchMatchesDisabledManagedMapping(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(*testing.T, *SqlDb, db.Project, db.User, db.Role, time.Time)
		table string
	}{{"ldap", setupLDAPWorkflowRoleIdentity, "ldap_group_mapping"}, {"oidc", setupOIDCWorkflowRoleIdentity, "oidc_group_mapping"}} {
		t.Run(tt.name, func(t *testing.T) {
			store := InitConfigCreateTestStore()
			t.Cleanup(store.Close)
			projectID, repositoryID := newTemplateTestProject(t, store)
			project, err := store.GetProject(projectID)
			require.NoError(t, err)
			user, err := store.CreateUserWithoutPassword(db.User{Username: "overlay-" + tt.name, Name: "Overlay", Email: "overlay-" + tt.name + "@example.invalid"})
			require.NoError(t, err)
			role, err := store.CreateProjectRole(db.Role{ID: db.ProjectRoleID("overlay_managed_" + tt.name), Slug: "overlay_managed_" + tt.name, Name: "Managed", ProjectID: &projectID, Permissions: db.CanViewProjectResources, Revision: 1})
			require.NoError(t, err)
			tt.setup(t, store, project, user, role, time.Unix(1900000000, 0).UTC())
			template, err := store.CreateTemplate(db.Template{ProjectID: projectID, RepositoryID: repositoryID, Name: "Template", Playbook: "site.yml"})
			require.NoError(t, err)
			_, err = store.exec("update "+tt.table+" set enabled=false where id=?", "workflow-approvers")
			require.NoError(t, err)
			visible, err := store.userCanViewTask(projectID, template.ID, nil, user.ID)
			require.NoError(t, err)
			viewers, err := store.GetTaskViewers(projectID, template.ID, nil)
			require.NoError(t, err)
			assert.Equal(t, visible, containsViewerID(viewers, user.ID))
		})
	}
}
func containsViewerID(ids []int, id int) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestTemplateViewerBatchMatchesLegacyMembershipRevision(t *testing.T) {
	f := newVisibilityFixture(t)
	user, err := f.store.CreateUserWithoutPassword(db.User{Username: "overlay-builtin", Name: "Overlay builtin", Email: "overlay-builtin@example.invalid"})
	require.NoError(t, err)
	_, err = f.store.CreateProjectUser(db.ProjectUser{ProjectID: f.projectID, UserID: user.ID, Role: db.ProjectGuest, Revision: 1})
	require.NoError(t, err)
	_, err = f.store.exec("update project__user set revision=0 where project_id=? and user_id=?", f.projectID, user.ID)
	require.NoError(t, err)
	visible, err := f.store.userCanViewTask(f.projectID, f.a.ID, nil, user.ID)
	require.NoError(t, err)
	viewers, err := f.store.GetTaskViewers(f.projectID, f.a.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, visible, containsViewerID(viewers, user.ID))
}

func TestVisibilityPropagatesDatabaseErrors(t *testing.T) {
	f := newVisibilityFixture(t)
	f.store.Close()
	_, err := f.store.userCanViewTask(f.projectID, f.a.ID, nil, f.reader.ID)
	require.Error(t, err)
	_, err = f.store.userCanViewTask(f.projectID, f.b.ID, f.nodeTask.WorkflowRunID, f.reader.ID)
	require.Error(t, err)
}
