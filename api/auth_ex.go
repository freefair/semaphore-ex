package api

import (
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"net/http"
)

func globalPermissionMiddleware(permission db.GlobalPermission) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := helpers.GetFromContext(r, "user").(*db.User)
			allowed, err := hasGlobalPermission(r, user, permission)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}
			if !allowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func hasGlobalPermission(
	r *http.Request,
	user *db.User,
	permission db.GlobalPermission,
) (bool, error) {
	if user == nil {
		return false, nil
	}
	// The built-in administrator remains the break-glass authority even when no
	// enhanced role assignment can be read yet (for example during recovery).
	if user.Admin {
		return true, nil
	}
	permissions, err := helpers.Store(r).GetEffectiveGlobalPermissions(user.ID)
	if err != nil {
		return false, err
	}
	if !permissions.Can(permission) {
		return false, nil
	}
	snapshot, ok := capabilitySnapshotFromHTTP(r)
	if !ok {
		return false, nil
	}
	if err := snapshot.Require(pro_interfaces.CapabilityProjectRoles, capabilityAccessForMethod(r.Method)); err != nil {
		return false, nil
	}
	return true, nil
}
