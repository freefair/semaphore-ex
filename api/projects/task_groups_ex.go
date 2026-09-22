package projects

import (
	"net/http"
	"sort"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

// TaskGroupController exposes only catalog operations. Template selection is
// validated separately through ResolveTaskGroups before the template is saved.
type TaskGroupController struct {
	groups   db.TaskGroupManager
	runners  taskGroupRunnerCatalog
	projects taskGroupProjectCatalog
}

// taskGroupRunnerCatalog deliberately exposes only the two listing queries
// needed by group-policy editing; its results are converted to a safe DTO.
type taskGroupRunnerCatalog interface {
	GetRunners(int, bool, db.RunnerTagFilterMode, *string) ([]db.Runner, error)
	GetAllRunners(bool, bool, db.RunnerTagFilterMode, *string) ([]db.Runner, error)
}

type taskGroupProjectCatalog interface {
	GetProjects(int) ([]db.Project, error)
	GetAllProjects() ([]db.Project, error)
}

func NewTaskGroupController(groups db.TaskGroupManager, runners ...taskGroupRunnerCatalog) *TaskGroupController {
	controller := &TaskGroupController{groups: groups}
	if len(runners) > 0 {
		controller.runners = runners[0]
	}
	return controller
}

func (c *TaskGroupController) ConfigureProjectCatalog(projects taskGroupProjectCatalog) {
	c.projects = projects
}

// TaskGroupRunnerOption is intentionally limited to policy-selection metadata.
// In particular, it never serializes registration tokens, webhook endpoints,
// public keys, health, or executor configuration.
type TaskGroupRunnerOption struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	ProjectID *int     `json:"project_id"`
	Tags      []string `json:"tags"`
}

func (c *TaskGroupController) GetTaskGroups(w http.ResponseWriter, r *http.Request) {
	if c.groups == nil {
		helpers.WriteErrorStatus(w, "task group catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	groups, err := c.groups.GetTaskGroups(project.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, groups)
}

func (c *TaskGroupController) GetTaskGroup(w http.ResponseWriter, r *http.Request) {
	if c.groups == nil {
		helpers.WriteErrorStatus(w, "task group catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	groupID, ok := helpers.GetIntParamOrAbort("group_id", w, r)
	if !ok {
		return
	}
	group, err := c.groups.GetTaskGroup(project.ID, groupID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, group)
}

func (c *TaskGroupController) GetTaskGroupRunners(w http.ResponseWriter, r *http.Request) {
	if c.runners == nil {
		helpers.WriteErrorStatus(w, "task group runner catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	projectRunners, err := c.runners.GetRunners(project.ID, false, db.RunnerFilterIgnoreTags, nil)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	globalRunners, err := c.runners.GetAllRunners(false, true, db.RunnerFilterIgnoreTags, nil)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	seen := make(map[int]struct{}, len(projectRunners)+len(globalRunners))
	result := make([]TaskGroupRunnerOption, 0, len(projectRunners)+len(globalRunners))
	for _, runner := range append(projectRunners, globalRunners...) {
		if _, duplicate := seen[runner.ID]; duplicate {
			continue
		}
		seen[runner.ID] = struct{}{}
		result = append(result, TaskGroupRunnerOption{
			ID: runner.ID, Name: runner.Name, ProjectID: runner.ProjectID,
			Tags: append([]string(nil), runner.Tags...),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *TaskGroupController) CreateTaskGroup(w http.ResponseWriter, r *http.Request) {
	if c.groups == nil {
		helpers.WriteErrorStatus(w, "task group catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	var group db.TaskGroup
	if !helpers.Bind(w, r, &group) {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	group.ID = 0
	group.ProjectID = project.ID
	if len(group.SharedProjectIDs) > 0 && !requireTaskGroupSharePermission(w, r) {
		return
	}
	if !c.requireVisibleTaskGroupShareTargets(w, r, group.SharedProjectIDs) {
		return
	}
	created, err := c.groups.CreateTaskGroup(group)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, created)
}

func (c *TaskGroupController) UpdateTaskGroup(w http.ResponseWriter, r *http.Request) {
	if c.groups == nil {
		helpers.WriteErrorStatus(w, "task group catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	groupID, ok := helpers.GetIntParamOrAbort("group_id", w, r)
	if !ok {
		return
	}
	var group db.TaskGroup
	if !helpers.Bind(w, r, &group) {
		return
	}
	if group.ID != 0 && group.ID != groupID {
		helpers.WriteErrorStatus(w, "task group ID does not match route", http.StatusBadRequest)
		return
	}
	group.ID = groupID
	group.ProjectID = project.ID
	current, err := c.groups.GetTaskGroup(project.ID, groupID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if !taskGroupBindingsEqual(current.SharedProjectIDs, group.SharedProjectIDs) &&
		!requireTaskGroupSharePermission(w, r) {
		return
	}
	if !c.requireVisibleTaskGroupShareTargets(w, r, taskGroupBindingAdditions(current.SharedProjectIDs, group.SharedProjectIDs)) {
		return
	}
	if _, err := c.groups.UpdateTaskGroup(group); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *TaskGroupController) DeleteTaskGroup(w http.ResponseWriter, r *http.Request) {
	if c.groups == nil {
		helpers.WriteErrorStatus(w, "task group catalog is unavailable", http.StatusServiceUnavailable)
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	groupID, ok := helpers.GetIntParamOrAbort("group_id", w, r)
	if !ok {
		return
	}
	var request struct {
		Revision int `json:"revision"`
	}
	if !helpers.Bind(w, r, &request) {
		return
	}
	if err := c.groups.DeleteTaskGroup(project.ID, groupID, request.Revision); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func requireTaskGroupSharePermission(w http.ResponseWriter, r *http.Request) bool {
	permissions := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	if permissions&db.CanShareTaskGroups == db.CanShareTaskGroups {
		return true
	}
	w.WriteHeader(http.StatusForbidden)
	return false
}

func taskGroupBindingsEqual(left, right db.TaskGroupBindings) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func taskGroupBindingAdditions(current, requested db.TaskGroupBindings) db.TaskGroupBindings {
	additions := make(db.TaskGroupBindings, 0, len(requested))
	for _, id := range requested {
		found := false
		for _, existing := range current {
			if id == existing {
				found = true
				break
			}
		}
		if !found {
			additions = append(additions, id)
		}
	}
	return additions
}

// requireVisibleTaskGroupShareTargets prevents a project role from granting a
// group to an arbitrary project ID. The target must appear in the same visible
// project catalog used by the share selector; administrators may see all.
func (c *TaskGroupController) requireVisibleTaskGroupShareTargets(w http.ResponseWriter, r *http.Request, targets db.TaskGroupBindings) bool {
	if len(targets) == 0 {
		return true
	}
	if c.projects == nil {
		helpers.WriteErrorStatus(w, "project catalog is unavailable", http.StatusServiceUnavailable)
		return false
	}
	user := helpers.UserFromContext(r)
	if user == nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	var projects []db.Project
	var err error
	if user.Admin {
		projects, err = c.projects.GetAllProjects()
	} else {
		projects, err = c.projects.GetProjects(user.ID)
	}
	if err != nil {
		helpers.WriteError(w, err)
		return false
	}
	visible := make(map[int]struct{}, len(projects))
	for _, project := range projects {
		visible[project.ID] = struct{}{}
	}
	for _, target := range targets {
		if _, ok := visible[target]; !ok {
			w.WriteHeader(http.StatusForbidden)
			return false
		}
	}
	return true
}
