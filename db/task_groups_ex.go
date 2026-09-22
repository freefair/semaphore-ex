package db

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// TaskGroup is owned by one project. Explicit grants allow other projects to
// select it, without transferring policy or grant administration.
type TaskGroup struct {
	ID               int               `db:"id" json:"id"`
	ProjectID        int               `db:"project_id" json:"project_id"`
	Name             string            `db:"name" json:"name"`
	Description      string            `db:"description" json:"description"`
	MaxParallelTasks int               `db:"max_parallel_tasks" json:"max_parallel_tasks"`
	RunnerIDs        TaskGroupBindings `db:"runner_ids" json:"runner_ids"`
	SharedProjectIDs TaskGroupBindings `db:"-" json:"shared_project_ids"`
	Revision         int               `db:"revision" json:"revision"`
}

// TaskGroupManager owns group visibility, policy and reference validation.
type TaskGroupManager interface {
	GetTaskGroups(projectID int) ([]TaskGroup, error)
	GetTaskGroup(projectID, groupID int) (TaskGroup, error)
	CreateTaskGroup(group TaskGroup) (TaskGroup, error)
	UpdateTaskGroup(group TaskGroup) (TaskGroup, error)
	DeleteTaskGroup(projectID, groupID, revision int) error
	ResolveTaskGroups(projectID int, ids TaskGroupBindings) ([]TaskGroup, error)
}

// TaskGroupBindings contains stable identifiers, never free-text group names.
type TaskGroupBindings []int

func (bindings *TaskGroupBindings) Scan(value any) error {
	if value == nil {
		*bindings = nil
		return nil
	}
	var payload []byte
	switch v := value.(type) {
	case []byte:
		payload = v
	case string:
		payload = []byte(v)
	default:
		return errors.New("unsupported group identifier value")
	}
	return json.Unmarshal(payload, bindings)
}
func (bindings TaskGroupBindings) Value() (driver.Value, error) {
	if bindings == nil {
		return nil, nil
	}
	return json.Marshal(bindings)
}

func NormalizeTaskGroups(bindings TaskGroupBindings) (TaskGroupBindings, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	if len(bindings) > 16 {
		return nil, errors.New("a template may select at most 16 task groups")
	}
	return normalizeGroupIDs(bindings)
}
func normalizeGroupIDs(ids TaskGroupBindings) (TaskGroupBindings, error) {
	result := slices.Clone(ids)
	sort.Ints(result)
	for i, id := range result {
		if id <= 0 {
			return nil, errors.New("group, project and runner identifiers must be positive")
		}
		if i > 0 && result[i-1] == id {
			return nil, errors.New("duplicate identifier")
		}
	}
	return result, nil
}
func (g *TaskGroup) Validate() error {
	g.Name = strings.TrimSpace(g.Name)
	if g.ProjectID <= 0 || g.Name == "" || len(g.Name) > 128 || strings.ContainsAny(g.Name, "\r\n\x00") {
		return errors.New("task group requires a project and a name of 1–128 characters")
	}
	if len(g.Description) > 2048 {
		return errors.New("task group description exceeds 2048 characters")
	}
	if g.MaxParallelTasks < 1 || g.MaxParallelTasks > 1000 {
		return errors.New("task group concurrent executions must be between 1 and 1000")
	}
	var err error
	g.RunnerIDs, err = normalizeGroupIDs(g.RunnerIDs)
	if err != nil {
		return err
	}
	g.SharedProjectIDs, err = normalizeGroupIDs(g.SharedProjectIDs)
	if err != nil {
		return err
	}
	for _, id := range g.SharedProjectIDs {
		if id == g.ProjectID {
			return errors.New("a task group already belongs to its owner project")
		}
	}
	return nil
}

// TaskGroupKeys freezes membership identifiers for the queued execution.
func TaskGroupKeys(groups []TaskGroup) StringArrayField {
	keys := make(StringArrayField, 0, len(groups))
	for _, group := range groups {
		keys = append(keys, fmt.Sprintf("group/%d", group.ID))
	}
	return keys
}

// IntersectTaskGroupRunners applies every group policy. Nil means unrestricted;
// a non-empty set constrains dispatch. Contradictory definitions are an error,
// independently of runner liveness or momentary capacity.
func IntersectTaskGroupRunners(groups []TaskGroup) (TaskGroupBindings, error) {
	var allowed TaskGroupBindings
	for _, group := range groups {
		if len(group.RunnerIDs) == 0 {
			continue
		}
		if allowed == nil {
			allowed = slices.Clone(group.RunnerIDs)
			continue
		}
		intersection := make(TaskGroupBindings, 0, len(allowed))
		for _, id := range allowed {
			if slices.Contains(group.RunnerIDs, id) {
				intersection = append(intersection, id)
			}
		}
		if len(intersection) == 0 {
			return nil, errors.New("task group runner requirements have no common runner")
		}
		allowed = intersection
	}
	return allowed, nil
}
