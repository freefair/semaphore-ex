package projects

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/semaphoreui/semaphore/util"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

// TemplatesMiddleware ensures a template exists and loads it to the context
func TemplatesMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		templateID, ok := helpers.GetIntParamOrAbort("template_id", w, r)
		if !ok {
			return
		}

		template, err := helpers.Store(r).GetTemplate(project.ID, templateID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		r = helpers.SetContextValue(r, "template", template)
		next.ServeHTTP(w, r)
	})
}

type TemplateController struct {
	templateRepo              db.TemplateManager
	roleRepo                  db.RoleRepository
	executorImageAvailable    func(*db.User) bool
	crossProjectDeletionGuard interface {
		HasUnrevokedCrossProjectTemplateGrants(int, int) (bool, error)
	}
	crossProjectDeletionStore interface {
		DeleteTemplateWithCrossProjectGrantGuard(int, int) error
	}
}

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

func NewTemplateController(
	templateRepo db.TemplateManager,
	roleRepo db.RoleRepository,
	executorImageResolvers ...func(*db.User) bool,
) *TemplateController {
	controller := &TemplateController{
		templateRepo: templateRepo,
		roleRepo:     roleRepo,
	}
	if len(executorImageResolvers) > 0 {
		controller.executorImageAvailable = executorImageResolvers[0]
	}
	return controller
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

// GetTemplate returns single template by ID
func GetTemplate(w http.ResponseWriter, r *http.Request) {
	template := helpers.GetFromContext(r, "template").(db.Template)
	permissions := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	res := db.TemplateWithPerms{
		Template:    template,
		Permissions: &permissions,
	}
	helpers.WriteJSON(w, http.StatusOK, res)
}

func GetTemplateRefs(w http.ResponseWriter, r *http.Request) {
	tpl := helpers.GetFromContext(r, "template").(db.Template)
	refs, err := helpers.Store(r).GetTemplateRefs(tpl.ProjectID, tpl.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, refs)
}

// GetTemplates returns all templates for a project in a sort order
func GetTemplates(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	user := helpers.UserFromContext(r)
	filter := db.TemplateFilter{}
	if r.URL.Query().Get("app") != "" {
		app := db.TemplateApp(r.URL.Query().Get("app"))
		filter.App = &app
	}
	params, err := applyTemplateSearchQuery(r, &filter)
	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}
	templates, err := helpers.Store(r).GetTemplatesWithPermissions(project.ID, user.ID, filter, params)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, templates)
}

func applyTemplateSearchQuery(r *http.Request, filter *db.TemplateFilter) (db.RetrieveQueryParams, error) {
	const maxTemplatePageSize = 200

	search, present := r.URL.Query()["search"]
	if present {
		if len(search) != 1 {
			return db.RetrieveQueryParams{}, errors.New("template search must be specified once")
		}
		filter.Search = search[0]
	}
	if err := filter.ValidateSearch(); err != nil {
		return db.RetrieveQueryParams{}, err
	}

	params := helpers.QueryParams(r.URL)
	countPresent := false
	offsetPresent := false
	for _, field := range []struct {
		key         string
		destination *int
	}{
		{key: "count", destination: &params.Count},
		{key: "offset", destination: &params.Offset},
	} {
		raw, present := r.URL.Query()[field.key]
		if !present {
			continue
		}
		if field.key == "count" {
			countPresent = true
		} else {
			offsetPresent = true
		}
		if len(raw) != 1 || raw[0] == "" {
			return db.RetrieveQueryParams{}, errors.New("template " + field.key + " must be an integer")
		}
		value, err := strconv.Atoi(raw[0])
		if err != nil {
			return db.RetrieveQueryParams{}, errors.New("template " + field.key + " must be an integer")
		}
		if field.key == "count" && (value <= 0 || value > maxTemplatePageSize) {
			return db.RetrieveQueryParams{}, errors.New("template count must be between 1 and 200")
		}
		*field.destination = value
	}
	if offsetPresent && !countPresent {
		return db.RetrieveQueryParams{}, errors.New("template offset requires count")
	}
	if _, err := params.Validate(db.TemplateProps); err != nil {
		return db.RetrieveQueryParams{}, err
	}
	return params, nil
}

// AddTemplate adds a template to the database
func AddTemplate(w http.ResponseWriter, r *http.Request) {
	addTemplate(w, r, nil)
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

func UpdateTemplateDescription(w http.ResponseWriter, r *http.Request) {
	template := helpers.GetFromContext(r, "template").(db.Template)

	var tpl struct {
		Description string `json:"description"`
	}

	if !helpers.Bind(w, r, &tpl) {
		return
	}

	err := helpers.Store(r).SetTemplateDescription(template.ProjectID, template.ID, tpl.Description)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   template.ProjectID,
		ObjectType:  db.EventTemplate,
		ObjectID:    template.ID,
		Description: fmt.Sprintf("Template ID %d description updated", template.ID),
	})

	w.WriteHeader(http.StatusNoContent)
}

// UpdateTemplate writes a template to an existing key in the database
func UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	updateTemplate(w, r, nil)
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

// RemoveTemplate deletes a template from the database
func RemoveTemplate(w http.ResponseWriter, r *http.Request) {
	removeTemplate(w, r, nil, nil)
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

func SetTemplateInventory(w http.ResponseWriter, r *http.Request) {
	tpl := helpers.GetFromContext(r, "template").(db.Template)
	inv := helpers.GetFromContext(r, "inventory").(db.Inventory)

	if !tpl.App.HasInventoryType(inv.Type) {
		helpers.WriteErrorStatus(w, "Inventory type is not supported for this template", http.StatusBadRequest)
		return
	}

	if tpl.App.IsTerraform() && (inv.TemplateID == nil || *inv.TemplateID != tpl.ID) {
		helpers.WriteErrorStatus(w, "Inventory is not attached to this template", http.StatusBadRequest)
		return
	}

	tpl.InventoryID = &inv.ID
	err := helpers.Store(r).UpdateTemplate(tpl)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func AttachInventory(w http.ResponseWriter, r *http.Request) {
	tpl := helpers.GetFromContext(r, "template").(db.Template)
	inv := helpers.GetFromContext(r, "inventory").(db.Inventory)

	if inv.TemplateID != nil {
		helpers.WriteErrorStatus(w, "Inventory is already attached to another template", http.StatusBadRequest)
		return
	}

	if !tpl.App.HasInventoryType(inv.Type) {
		helpers.WriteErrorStatus(w, "Inventory type is not supported for this template", http.StatusBadRequest)
		return
	}

	inv.TemplateID = &tpl.ID
	err := helpers.Store(r).UpdateInventory(inv)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func DetachInventory(w http.ResponseWriter, r *http.Request) {
	tpl := helpers.GetFromContext(r, "template").(db.Template)
	inv := helpers.GetFromContext(r, "inventory").(db.Inventory)

	if inv.TemplateID == nil || *inv.TemplateID != tpl.ID {
		helpers.WriteErrorStatus(w, "Inventory is not attached to this template", http.StatusBadRequest)
		return
	}

	inv.TemplateID = nil
	err := helpers.Store(r).UpdateInventory(inv)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *TemplateController) GetTemplatePerms(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	tpl := helpers.GetFromContext(r, "template").(db.Template)

	perms, err := helpers.Store(r).GetTemplateRoles(project.ID, tpl.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, perms)
}

func (c *TemplateController) AddTemplatePerm(w http.ResponseWriter, r *http.Request) {
	template := helpers.GetFromContext(r, "template").(db.Template)

	var perm db.TemplateRolePerm
	if !helpers.Bind(w, r, &perm) {
		return
	}

	perm.ProjectID = template.ProjectID
	perm.TemplateID = template.ID

	newPerm, err := c.templateRepo.CreateTemplateRole(perm)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusCreated, newPerm)
}

func (c *TemplateController) UpdateTemplatePerm(w http.ResponseWriter, r *http.Request) {
	template := helpers.GetFromContext(r, "template").(db.Template)
	permID, ok := helpers.GetIntParamOrAbort("perm_id", w, r)
	if !ok {
		return
	}

	var perm db.TemplateRolePerm
	if !helpers.Bind(w, r, &perm) {
		return
	}

	perm.ID = permID
	perm.ProjectID = template.ProjectID
	perm.TemplateID = template.ID

	if perm.Revision <= 0 {
		helpers.WriteErrorStatus(w, "A positive template permission revision is required", http.StatusBadRequest)
		return
	}

	updated, err := c.templateRepo.UpdateTemplateRole(perm, perm.Revision)
	if err != nil {
		writeTemplateRoleError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *TemplateController) DeleteTemplatePerm(w http.ResponseWriter, r *http.Request) {
	template := helpers.GetFromContext(r, "template").(db.Template)
	permID, ok := helpers.GetIntParamOrAbort("perm_id", w, r)
	if !ok {
		return
	}

	revision, revisionErr := strconv.Atoi(r.URL.Query().Get("revision"))
	if revisionErr != nil || revision <= 0 {
		helpers.WriteErrorStatus(w, "A positive template permission revision is required", http.StatusBadRequest)
		return
	}
	err := c.templateRepo.DeleteTemplateRole(template.ProjectID, template.ID, permID, revision)
	if err != nil {
		writeTemplateRoleError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *TemplateController) GetTemplatePerm(w http.ResponseWriter, r *http.Request) {
	template := helpers.GetFromContext(r, "template").(db.Template)
	permID, ok := helpers.GetIntParamOrAbort("perm_id", w, r)
	if !ok {
		return
	}

	perm, err := c.templateRepo.GetTemplateRole(template.ProjectID, template.ID, permID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, perm)
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
