package projects

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

func resolveProjectMembershipRole(
	w http.ResponseWriter,
	r *http.Request,
	projectID int,
	roleRef db.ProjectUserRole,
) (db.ProjectUserRole, *db.ProjectRoleID, bool) {
	if roleRef.IsValid() {
		return roleRef, nil, true
	}
	roleID := db.ProjectRoleID(roleRef)
	role, err := helpers.Store(r).GetProjectRoleByID(projectID, roleID)
	if err == nil {
		return db.ProjectNone, &role.ID, true
	}
	if !errors.Is(err, db.ErrNotFound) {
		helpers.WriteError(w, err)
		return db.ProjectNone, nil, false
	}
	role, err = helpers.Store(r).GetProjectOrGlobalRoleBySlug(projectID, string(roleRef))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return db.ProjectNone, nil, false
	}
	if role.ProjectID != nil {
		return db.ProjectNone, &role.ID, true
	}
	return db.ProjectUserRole(role.Slug), nil, true
}

func writeProjectMembershipError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrLastProjectAdministrator):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "LAST_PROJECT_ADMINISTRATOR", "message": err.Error(),
		})
	case errors.Is(err, db.ErrProjectMembershipRevisionConflict):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "PROJECT_MEMBERSHIP_REVISION_CONFLICT", "message": err.Error(),
		})
	default:
		helpers.WriteError(w, err)
	}
}
