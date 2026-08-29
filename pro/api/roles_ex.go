package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"net/http"
)

func (c *RolesController) GetProjectPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}
