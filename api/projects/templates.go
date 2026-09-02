package projects

import (
	"fmt"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
	"strconv"
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

// AddTemplate adds a template to the database
func AddTemplate(w http.ResponseWriter, r *http.Request) {
	addTemplate(w, r, nil)
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

// RemoveTemplate deletes a template from the database
func RemoveTemplate(w http.ResponseWriter, r *http.Request) {
	removeTemplate(w, r, nil, nil)
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
