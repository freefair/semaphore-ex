package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"net/http"
)

func (c *RolesController) GetGlobalPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}

func (c *RolesController) GetGlobalRoleAssignments(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}

func (c *RolesController) AddGlobalRoleAssignment(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) DeleteGlobalRoleAssignment(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) GetEffectiveGlobalPermissions(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) GetProjectPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}
