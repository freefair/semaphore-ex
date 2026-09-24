package projects

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

func (c *TaskController) inventoryHostRepository(w http.ResponseWriter) db.InventoryHostRepository {
	repository, ok := c.ansibleTaskRepo.(db.InventoryHostRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Inventory host storage is unavailable", http.StatusServiceUnavailable)
		return nil
	}
	return repository
}

func (c *TaskController) canReadHostTask(r *http.Request, taskID int) bool {
	project := helpers.GetFromContext(r, "project").(db.Project)
	user := helpers.UserFromContext(r)
	if user == nil {
		return false
	}
	task, err := c.store.GetTask(project.ID, taskID)
	if err != nil {
		return false
	}
	permissions, err := c.store.GetTemplatePermissionContext(project.ID, task.TemplateID, user.ID)
	return err == nil && permissions.EffectivePermissions.Can(db.CanReadTemplate) && c.authorizeWorkflowTask(r, task, pro_interfaces.PermissionViewWorkflow)
}

// GetInventoryHosts returns only complete snapshots whose originating task is
// visible to this actor. Pagination refills after authorization filtering.
func (c *TaskController) GetInventoryHosts(w http.ResponseWriter, r *http.Request) {
	repository := c.inventoryHostRepository(w)
	if repository == nil {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	values := r.URL.Query()
	query := db.InventoryHostQuery{Search: strings.TrimSpace(values.Get("search")), AfterHost: values.Get("after_host"), Count: 51}
	query.InventoryID, _ = strconv.Atoi(values.Get("inventory_id"))
	query.AfterInventoryID, _ = strconv.Atoi(values.Get("after_inventory_id"))
	if len(query.Search) > 255 || len(query.AfterHost) > 255 || query.InventoryID < 0 || query.AfterInventoryID < 0 {
		helpers.WriteErrorStatus(w, "Invalid host query", http.StatusBadRequest)
		return
	}
	result := db.InventoryHostPage{Items: []db.InventoryHost{}}
	visibility := map[int]bool{}
	for {
		page, err := repository.GetInventoryHosts(project.ID, query)
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		for _, host := range page.Items {
			visible, checked := visibility[host.TaskID]
			if !checked {
				visible = c.canReadHostTask(r, host.TaskID)
				visibility[host.TaskID] = visible
			}
			if visible {
				result.Items = append(result.Items, host)
			}
			if len(result.Items) > 50 {
				result.Items = result.Items[:50]
				last := result.Items[49]
				result.NextInventoryID, result.NextHost = last.InventoryID, last.Host
				helpers.WriteJSON(w, http.StatusOK, result)
				return
			}
		}
		if page.NextHost == "" {
			break
		}
		query.AfterInventoryID, query.AfterHost = page.NextInventoryID, page.NextHost
	}
	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *TaskController) GetInventoryHostSnapshots(w http.ResponseWriter, r *http.Request) {
	repository := c.inventoryHostRepository(w)
	if repository == nil {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	inventoryID, ok := helpers.GetIntParamOrAbort("inventory_id", w, r)
	if !ok {
		return
	}
	items, err := repository.GetInventoryHostSnapshots(project.ID, inventoryID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	visible := []db.InventoryHostSnapshot{}
	for _, snapshot := range items {
		if c.canReadHostTask(r, snapshot.TaskID) {
			visible = append(visible, snapshot)
		}
	}
	helpers.WriteJSON(w, http.StatusOK, visible)
}

func (c *TaskController) GetHostTasks(w http.ResponseWriter, r *http.Request) {
	repository := c.inventoryHostRepository(w)
	if repository == nil {
		return
	}
	project := helpers.GetFromContext(r, "project").(db.Project)
	values := r.URL.Query()
	inventoryID, err := strconv.Atoi(values.Get("inventory_id"))
	host := values.Get("host")
	if err != nil || inventoryID <= 0 || len(host) == 0 || len(host) > 255 {
		helpers.WriteErrorStatus(w, "Inventory and host are required", http.StatusBadRequest)
		return
	}
	before, _ := strconv.Atoi(values.Get("before"))
	items := []db.TaskWithTpl{}
	for len(items) <= 20 {
		ids, queryErr := repository.GetHostTaskIDs(project.ID, inventoryID, host, before, 50)
		if queryErr != nil {
			helpers.WriteError(w, queryErr)
			return
		}
		for _, id := range ids {
			before = id
			if !c.canReadHostTask(r, id) {
				continue
			}
			task, taskErr := c.store.GetTask(project.ID, id)
			if taskErr != nil {
				helpers.WriteError(w, taskErr)
				return
			}
			template, templateErr := c.store.GetTemplate(project.ID, task.TemplateID)
			if templateErr != nil {
				helpers.WriteError(w, templateErr)
				return
			}
			item := db.TaskWithTpl{Task: task, TemplateAlias: template.Name, TemplateApp: template.App, TemplatePlaybook: template.Playbook}
			if task.UserID != nil {
				if user, userErr := c.store.GetUser(*task.UserID); userErr == nil {
					item.UserName = &user.Name
				}
			}
			items = append(items, item)
			if len(items) > 20 {
				break
			}
		}
		if len(ids) < 50 {
			break
		}
	}
	page := db.TaskSummaryPage[db.TaskWithTpl]{Items: items}
	if len(items) > 20 {
		page.Items = items[:20]
		cursor := page.Items[19].ID
		page.NextCursor = &cursor
	}
	helpers.WriteJSON(w, http.StatusOK, page)
}
