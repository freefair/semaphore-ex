package projects

import (
	"fmt"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"net/http"
)

func validateTemplateExecutorImage(
	w http.ResponseWriter,
	r *http.Request,
	template *db.Template,
	available func(*db.User) bool,
) bool {
	if template.ExecutorImage == nil {
		return true
	}
	image, err := db.NormalizeExecutorImage(*template.ExecutorImage)
	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return false
	}
	template.ExecutorImage = image
	if image == nil {
		return true
	}
	if available == nil || !available(helpers.UserFromContext(r)) {
		helpers.WriteErrorStatus(w, db.ErrExecutorImageCapabilityUnavailable.Error(), http.StatusForbidden)
		return false
	}
	return true
}

func (c *TemplateController) AddTemplate(w http.ResponseWriter, r *http.Request) {
	addTemplate(w, r, c.executorImageAvailable)
}

func addTemplate(w http.ResponseWriter, r *http.Request, executorImageAvailable func(*db.User) bool) {
	project := helpers.GetFromContext(r, "project").(db.Project)

	var template db.Template
	if !helpers.Bind(w, r, &template) {
		return
	}
	if !validateTemplateExecutorImage(w, r, &template, executorImageAvailable) {
		return
	}

	var err error

	template.ProjectID = project.ID
	newTemplate, err := helpers.Store(r).CreateTemplate(template)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	// Check workspace and create it if required.
	if newTemplate.App.IsTerraform() {
		var inv db.Inventory

		if newTemplate.InventoryID == nil {
			var inventoryType db.InventoryType

			if invTypes := newTemplate.App.InventoryTypes(); len(invTypes) > 0 {
				inventoryType = invTypes[0]
			} else {
				helpers.WriteErrorStatus(w, "Inventory type is not supported for this template", http.StatusBadRequest)
				return
			}

			inv, err = helpers.Store(r).CreateInventory(db.Inventory{
				Name:       "default",
				ProjectID:  project.ID,
				TemplateID: &newTemplate.ID,
				Type:       inventoryType,
				Inventory:  "default",
			})

			if err != nil {
				helpers.WriteError(w, err)
				return
			}

			newTemplate.InventoryID = &inv.ID
			err = helpers.Store(r).UpdateTemplate(newTemplate)

		} else {
			inv, err = helpers.Store(r).GetInventory(project.ID, *newTemplate.InventoryID)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}

			inv.TemplateID = &newTemplate.ID
			err = helpers.Store(r).UpdateInventory(inv)
		}

		if err != nil {
			helpers.WriteError(w, err)
			return
		}
	}

	helpers.EventLog(r, helpers.EventLogCreate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   project.ID,
		ObjectType:  db.EventSchedule,
		ObjectID:    newTemplate.ID,
		Description: fmt.Sprintf("Template ID %d created", newTemplate.ID),
	})

	helpers.WriteJSON(w, http.StatusCreated, newTemplate)
}

func (c *TemplateController) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	updateTemplate(w, r, c.executorImageAvailable)
}

func updateTemplate(w http.ResponseWriter, r *http.Request, executorImageAvailable func(*db.User) bool) {
	oldTemplate := helpers.GetFromContext(r, "template").(db.Template)

	var template db.Template
	if !helpers.Bind(w, r, &template) {
		return
	}
	if !validateTemplateExecutorImage(w, r, &template, executorImageAvailable) {
		return
	}

	if _, ok := util.Config.Apps[string(template.App)]; !ok {
		helpers.WriteErrorStatus(w, "Invalid app id: "+string(template.App), http.StatusBadRequest)
		return
	}

	// project ID and template ID in the body and the path must be the same

	if template.ID != oldTemplate.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "template id in URL and in body must be the same",
		})
		return
	}

	if template.ProjectID != oldTemplate.ProjectID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "You can not move template to other project",
		})
		return
	}

	if template.Arguments != nil && *template.Arguments == "" {
		template.Arguments = nil
	}

	if template.Type != db.TemplateDeploy {
		template.BuildTemplateID = nil
	}

	if template.Type != db.TemplateBuild {
		template.StartVersion = nil
	}

	err := helpers.Store(r).UpdateTemplate(template)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   oldTemplate.ProjectID,
		ObjectType:  db.EventTemplate,
		ObjectID:    oldTemplate.ID,
		Description: fmt.Sprintf("Template ID %d updated", template.ID),
	})

	w.WriteHeader(http.StatusNoContent)
}
