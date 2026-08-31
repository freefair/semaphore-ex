package projects

import (
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"net/http"
)

// ConfigureCrossProjectDeletionGuard attaches the optional Enhanced grant
// guard. Community keeps the historical delete behavior because its workflow
// store does not implement this narrow capability.
func (c *TemplateController) ConfigureCrossProjectDeletionGuard(guard any) {
	if configured, ok := guard.(interface {
		HasUnrevokedCrossProjectTemplateGrants(int, int) (bool, error)
	}); ok {
		c.crossProjectDeletionGuard = configured
	}
	if configured, ok := guard.(interface {
		DeleteTemplateWithCrossProjectGrantGuard(int, int) error
	}); ok {
		c.crossProjectDeletionStore = configured
	}
}

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

func (c *TemplateController) RemoveTemplate(w http.ResponseWriter, r *http.Request) {
	removeTemplate(w, r, c.crossProjectDeletionGuard, c.crossProjectDeletionStore)
}

func removeTemplate(w http.ResponseWriter, r *http.Request, guard interface {
	HasUnrevokedCrossProjectTemplateGrants(int, int) (bool, error)
}, command interface {
	DeleteTemplateWithCrossProjectGrantGuard(int, int) error
}) {
	tpl := helpers.GetFromContext(r, "template").(db.Template)
	if command != nil {
		if err := command.DeleteTemplateWithCrossProjectGrantGuard(tpl.ProjectID, tpl.ID); err != nil {
			if errors.Is(err, db.ErrCrossProjectTemplateGrantUnrevokedReference) {
				helpers.WriteErrorStatus(w, err.Error(), http.StatusConflict)
				return
			}
			helpers.WriteError(w, err)
			return
		}
		writeTemplateDeleteEvent(w, r, tpl)
		return
	}
	if guard != nil {
		blocked, guardErr := guard.HasUnrevokedCrossProjectTemplateGrants(tpl.ProjectID, tpl.ID)
		if guardErr != nil {
			helpers.WriteError(w, guardErr)
			return
		}
		if blocked {
			helpers.WriteErrorStatus(w, db.ErrCrossProjectTemplateGrantUnrevokedReference.Error(), http.StatusConflict)
			return
		}
	}

	err := helpers.Store(r).DeleteTemplate(tpl.ProjectID, tpl.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	writeTemplateDeleteEvent(w, r, tpl)
}

func writeTemplateDeleteEvent(w http.ResponseWriter, r *http.Request, tpl db.Template) {
	helpers.EventLog(r, helpers.EventLogDelete, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   tpl.ProjectID,
		ObjectType:  db.EventTemplate,
		ObjectID:    tpl.ID,
		Description: fmt.Sprintf("Template ID %d deleted", tpl.ID),
	})

	w.WriteHeader(http.StatusNoContent)
}

// GetEffectiveTemplatePermissions explains the current user's effective
// template access without returning any unrelated role assignments.
func (c *TemplateController) GetEffectiveTemplatePermissions(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	template := helpers.GetFromContext(r, "template").(db.Template)
	user := helpers.UserFromContext(r)
	permissionContext, err := c.templateRepo.GetTemplatePermissionContext(
		project.ID, template.ID, user.ID,
	)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	projectGrant := &pro_interfaces.PermissionGrant{
		Scope: pro_interfaces.PermissionScopeProject, RoleID: permissionContext.RoleID,
		RoleName:    permissionContext.RoleName,
		Permissions: projectPermissionIDs(permissionContext.ProjectPermissions),
	}
	var templateOverride *pro_interfaces.PermissionOverride
	if permissionContext.Override != nil {
		templateOverride = &pro_interfaces.PermissionOverride{
			RoleID: permissionContext.RoleID, RoleName: permissionContext.RoleName,
			Allow: templatePermissionIDs(permissionContext.Override.AllowedPermissions),
			Deny:  templatePermissionIDs(permissionContext.Override.DeniedPermissions),
		}
	}

	catalog := pro_interfaces.TemplatePermissionCatalog()
	decisions := make([]pro_interfaces.EffectivePermissionDecision, 0, len(catalog))
	for _, definition := range catalog {
		decisions = append(decisions, pro_interfaces.EvaluatePermission(
			pro_interfaces.PermissionEvaluationRequest{
				Permission: definition.ID, Project: projectGrant, Template: templateOverride,
			},
		))
	}
	helpers.WriteJSON(w, http.StatusOK, pro_interfaces.EffectiveTemplatePermissions{
		Permissions: permissionContext.EffectivePermissions,
		Decisions:   decisions,
	})
}

func projectPermissionIDs(permissions db.ProjectUserPermission) []pro_interfaces.PermissionID {
	result := make([]pro_interfaces.PermissionID, 0)
	for _, definition := range pro_interfaces.ProjectPermissionCatalog() {
		if permissions.Can(definition.Permission) {
			result = append(result, definition.ID)
		}
	}
	return result
}

func templatePermissionIDs(permissions db.TemplatePermission) []pro_interfaces.PermissionID {
	result := make([]pro_interfaces.PermissionID, 0)
	for _, definition := range pro_interfaces.TemplatePermissionCatalog() {
		if permissions.Can(db.TemplatePermission(definition.Permission)) {
			result = append(result, definition.ID)
		}
	}
	return result
}

func writeTemplateRoleError(w http.ResponseWriter, err error) {
	if errors.Is(err, db.ErrTemplateRoleRevisionConflict) {
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "TEMPLATE_ROLE_REVISION_CONFLICT", "message": err.Error(),
		})
		return
	}
	helpers.WriteError(w, err)
}
