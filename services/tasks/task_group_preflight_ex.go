package tasks

import (
	"errors"

	"github.com/semaphoreui/semaphore/db"
)

// resolveTaskPreflightGroups resolves the template's managed memberships in
// the execution project. Cross-project template access alone never grants use
// of the owner's groups. The returned digest input includes the revisions that
// control runner eligibility and capacity, so a reviewed plan goes stale when
// group policy changes.
func (p *TaskPool) resolveTaskPreflightGroups(
	template db.Template,
	executionProjectID int,
) ([]db.TaskGroup, db.TaskGroupBindings, error) {
	if len(template.TaskGroups) == 0 {
		return nil, nil, nil
	}
	groupsStore, ok := p.store.(db.TaskGroupManager)
	if !ok {
		return nil, nil, errors.New("task group catalog is unavailable")
	}
	groups, err := groupsStore.ResolveTaskGroups(executionProjectID, template.TaskGroups)
	if err != nil {
		return nil, nil, err
	}
	allowed, err := db.IntersectTaskGroupRunners(groups)
	if err != nil {
		return nil, nil, err
	}
	return groups, allowed, nil
}

func taskGroupPreflightPolicyDigest(groups []db.TaskGroup, allowed db.TaskGroupBindings) string {
	state := make([]any, 0, len(groups)*5+1)
	for _, group := range groups {
		state = append(state, group.ID, group.ProjectID, group.Revision, group.MaxParallelTasks, group.RunnerIDs)
	}
	state = append(state, allowed)
	return hashPreflightComponent(state...)
}
