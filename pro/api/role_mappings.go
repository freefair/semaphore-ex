package api

import (
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

type RoleMappingsController struct{}

func NewRoleMappingsController(db.RoleMappingStore, db.Store) *RoleMappingsController {
	return &RoleMappingsController{}
}
func (*RoleMappingsController) GetMappings(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []string{})
}
func (*RoleMappingsController) AddMapping(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
func (*RoleMappingsController) DeleteMapping(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}
