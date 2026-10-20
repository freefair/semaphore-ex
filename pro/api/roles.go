package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
)

type RolesController struct {
	store db.Store
}

func NewRolesController(store db.Store, _ pro_interfaces.CapabilityProvider) *RolesController {
	return &RolesController{
		store: store,
	}
}

func (c *RolesController) GetGlobalRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) GetRoles(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}

func (c *RolesController) AddRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) UpdateRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) DeleteRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

// Project-specific role methods
func (c *RolesController) GetProjectRoles(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}

func (c *RolesController) GetProjectAndGlobalRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := c.store.GetSharedRoles()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	projectID, ok := helpers.GetIntParamOrAbort("project_id", w, r)
	if !ok {
		return
	}
	projectRoles, err := c.store.GetProjectRoles(projectID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	roles = append(roles, projectRoles...)
	helpers.WriteJSON(w, http.StatusOK, roles)
}

func (c *RolesController) AddProjectRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) GetProjectRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) UpdateProjectRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) DeleteProjectRole(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
