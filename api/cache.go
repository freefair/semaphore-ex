package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

// clearCache empties the server's temporary directory. The route is under systemAPI, so
// globalPermissionMiddleware(db.CanManageGlobalSystem) has already let the caller through: an admin or a
// holder of a global role with that permission. The handler must not re-check the admin flag.
func clearCache(w http.ResponseWriter, r *http.Request) {
	currentUser := helpers.GetFromContext(r, "user").(*db.User)

	allowed, err := hasGlobalPermission(r, currentUser, db.CanManageGlobalSystem)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if !allowed {
		helpers.WriteJSON(w, http.StatusForbidden, map[string]string{
			"error": "User must have global system management permission",
		})
		return
	}

	err = util.Config.ClearTmpDir()
	if err != nil {
		log.Error(err)
		helpers.WriteJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "Can not clear cache",
		})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
