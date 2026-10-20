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
				helpers.RecordDenied(r, globalPermissionName(permission), 0)
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func globalPermissionName(permission db.GlobalPermission) string {
	for _, definition := range db.GlobalPermissionCatalog() {
		if definition.Permission == permission {
			return definition.ID
		}
	}
	return "global_permission"
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
	storeValue, ok := helpers.GetOkFromContext(r, "store")
	store, validStore := storeValue.(db.Store)
	if !ok || !validStore || store == nil {
		// Direct/internal handler use has no request-scoped store. It must deny
		// the delegated path rather than panic or treating missing context as a
		// break-glass grant.
		return false, nil
	}
	permissions, err := store.GetEffectiveGlobalPermissions(user.ID)
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
