package projects

import (
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

// GetMustHaveBaseProjectPermissionMiddleware checks the project role before a
// template override is applied. Template ACL administration is policy control,
// not a template content action, so an edit-only override cannot administer it.
func GetMustHaveBaseProjectPermissionMiddleware(permission db.ProjectUserPermission) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := helpers.GetFromContext(r, "user").(*db.User)
			basePermissions, ok := helpers.GetFromContext(r, "basePermissions").(db.ProjectUserPermission)
			if !ok || (!user.Admin && !basePermissions.Can(permission)) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func applyEffectiveTemplatePermissions(
	projectPermissions db.ProjectUserPermission,
	templatePermissions db.ProjectUserPermission,
) db.ProjectUserPermission {
	const templateMappedProjectPermissions = db.CanRunProjectTasks |
		db.CanManageProjectResources | db.CanViewProjectResources
	return projectPermissions&^templateMappedProjectPermissions | templatePermissions
}

// GetMustHaveTemplatePermissionMiddleware enforces one independent template
// action. It runs after ProjectMiddleware and TemplatesMiddleware.
func GetMustHaveTemplatePermissionMiddleware(permission db.TemplatePermission) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			project := helpers.GetFromContext(r, "project").(db.Project)
			template := helpers.GetFromContext(r, "template").(db.Template)
			user := helpers.GetFromContext(r, "user").(*db.User)
			context, err := helpers.Store(r).GetTemplatePermissionContext(
				project.ID, template.ID, user.ID,
			)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}
			if !context.EffectivePermissions.Can(permission) {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			r = helpers.SetContextValue(r, "templatePermissionContext", context)
			next.ServeHTTP(w, r)
		})
	}
}

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
