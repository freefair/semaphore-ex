package projects

import (
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

// GetMustHavePermissionMiddleware enforces a permission for every HTTP method.
// It is used for resources whose visibility is itself permission-controlled.
func GetMustHavePermissionMiddleware(permissions db.ProjectUserPermission) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := helpers.GetFromContext(r, "user").(*db.User)
			userPermissions := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
			if !user.Admin && !userPermissions.Can(permissions) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
