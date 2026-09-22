package export

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

type TaskExporter struct {
	ValueMap[db.Task]
}

func (e *TaskExporter) load(store db.Store, exporter DataExporter, progress Progress) error {
	projs, err := exporter.getLoadedKeysInt(Project, GlobalScope)
	if err != nil {
		return err
	}

	for _, proj := range projs {
		tasksTmpl, err := store.GetProjectTasks(proj, db.RetrieveQueryParams{})
		if err != nil {
			return err
		}

		tasks := make([]db.Task, len(tasksTmpl))
		for i, task := range tasksTmpl {
			tasks[i] = task.Task
		}

		slices.Reverse(tasks)

		err = e.appendValues(tasks, strconv.Itoa(proj))
		if err != nil {
			return err
		}
	}

	return nil
}
func (e *TaskExporter) restore(store db.Store, exporter DataExporter, progress Progress) (err error) {
	return e.restoreValues(store, exporter, progress, e)
}

func (e *TaskExporter) restoreValue(val EntityObject[db.Task], store db.Store, exporter DataExporter) (err error) {
	old := val.value

	old.ProjectID, err = exporter.getNewKeyInt(Project, GlobalScope, old.ProjectID)
	if err != nil {
		return err
	}

	old.TemplateID, err = exporter.getNewKeyInt(Template, val.scope, old.TemplateID)
	if err != nil {
		return err
	}
	// Task group keys are derived runtime state. Never carry them over from an
	// import: a backup can originate from a different project and its keys could
	// otherwise claim arbitrary managed groups. Only waiting tasks
	// are dispatchable after restore, so only they receive fresh keys from the
	// remapped template.
	old.TaskGroupKeys = nil
	if old.Status == task_logger.TaskWaitingStatus {
		template, templateErr := store.GetTemplate(old.ProjectID, old.TemplateID)
		if templateErr != nil {
			return templateErr
		}
		if len(template.TaskGroups) > 0 {
			groups, ok := store.(db.TaskGroupManager)
			if !ok {
				return fmt.Errorf("task group catalog is unavailable")
			}
			resolved, resolveErr := groups.ResolveTaskGroups(template.ProjectID, template.TaskGroups)
			if resolveErr != nil {
				return resolveErr
			}
			old.TaskGroupKeys = db.TaskGroupKeys(resolved)
		}
	}

	old.InventoryID, err = exporter.getNewKeyIntRef(Inventory, val.scope, old.InventoryID, e)
	if err != nil {
		return err
	}

	old.ScheduleID, err = exporter.getNewKeyIntRef(Schedule, val.scope, old.ScheduleID, e)
	if err != nil {
		return err
	}

	old.UserID, err = exporter.getNewKeyIntRef(User, GlobalScope, old.UserID, e)
	if err != nil {
		return err
	}

	old.IntegrationID, err = exporter.getNewKeyIntRef(Integration, val.scope, old.IntegrationID, e)
	if err != nil {
		return err
	}

	old.BuildTaskID, err = exporter.getNewKeyIntRef(Task, val.scope, old.BuildTaskID, e)
	if err != nil {
		return err
	}
	old.SSHKeys, err = remapSSHKeyBindings(exporter, val.scope, old.SSHKeys)
	if err != nil {
		return err
	}

	newObj, err := store.CreateTask(old, 0)
	if err != nil {
		return err
	}

	return exporter.mapKeys(e.getName(), val.scope, old.GetDbKey(), newObj.GetDbKey())
}

func (e *TaskExporter) getName() string {
	return Task
}

func (e *TaskExporter) exportDependsOn() []string {
	return []string{Project}
}

func (e *TaskExporter) importDependsOn() []string {
	return []string{Project, AccessKey, Template, Inventory, Integration, Schedule, User}
}
