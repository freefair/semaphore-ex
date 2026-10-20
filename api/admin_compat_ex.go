package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
)

// adminMiddleware retains the built-in administrator boundary for legacy
// administrative routes. Delegated global-system grants are admitted only by
// the explicitly scoped globalPermissionMiddleware routes.
func adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := helpers.GetFromContext(r, "user").(*db.User)
		if !user.Admin {
			helpers.RecordDenied(r, "admin", 0)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
