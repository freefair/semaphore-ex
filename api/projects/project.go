package projects

import (
	"errors"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

// ProjectMiddleware ensures a project exists and loads it to the context
func ProjectMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := helpers.GetFromContext(r, "user").(*db.User)

		projectID, ok := helpers.GetIntParamOrAbort("project_id", w, r)

		if !ok {
			return
		}

		// check if user in project's team
		projectUser, err := helpers.Store(r).GetProjectUser(projectID, user.ID)

		if !user.Admin && err != nil {
			helpers.WriteError(w, err)
			return
		}

		project, err := helpers.Store(r).GetProject(projectID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		roleSlug := projectUser.Role
		roleName := string(roleSlug)

		permissions := roleSlug.GetPermissions()

		if projectUser.RoleID != nil {
			role, roleErr := helpers.Store(r).GetProjectRoleByID(projectID, *projectUser.RoleID)
			if roleErr != nil {
				helpers.WriteError(w, roleErr)
				return
			}
			roleSlug = db.ProjectUserRole(role.ID)
			roleName = role.Name
			permissions = role.Permissions
		}

		// Built-in roles are defined in code and are the source of truth for their
		// permissions. Only custom roles are resolved from the database, otherwise a
		// project role sharing a built-in slug (e.g. "manager") could override the
		// built-in permissions and escalate privileges.
		if projectUser.RoleID == nil && !roleSlug.IsValid() {
			role, err := helpers.Store(r).GetProjectOrGlobalRoleBySlug(projectID, string(projectUser.Role))

			if err == nil {
				roleSlug = db.ProjectUserRole(role.Slug)
				roleName = role.Name
				permissions = role.Permissions
			} else if !errors.Is(err, db.ErrNotFound) {
				helpers.WriteError(w, err)
				return
			}
		}

		basePermissions := permissions
		if helpers.HasParam("template_id", r) {
			templateID, templateOk := helpers.GetIntParamOrAbort("template_id", w, r)
			if !templateOk {
				return
			}
			var perm db.ProjectUserPermission
			perm, err = helpers.Store(r).GetTemplatePermission(project.ID, templateID, user.ID)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}

			permissions = applyEffectiveTemplatePermissions(permissions, perm)
		}

		r = helpers.SetContextValue(r, "projectUserRole", roleSlug)
		r = helpers.SetContextValue(r, "projectUserRoleName", roleName)
		r = helpers.SetContextValue(r, "basePermissions", basePermissions)
		r = helpers.SetContextValue(r, "permissions", permissions)
		r = helpers.SetContextValue(r, "project", project)
		next.ServeHTTP(w, r)
	})
}

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

// GetMustCanMiddleware ensures that the user has administrator rights
func GetMustCanMiddleware(permissions db.ProjectUserPermission) mux.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			me := helpers.GetFromContext(r, "user").(*db.User)
			// Template child routes enforce their own independent action below this
			// legacy broad-project guard. Applying the broad bit here would turn a
			// run-only override into an unusable grant.
			if helpers.HasParam("template_id", r) {
				next.ServeHTTP(w, r)
				return
			}

			userPerms := helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)

			can := (userPerms & permissions) == permissions

			if !me.Admin && r.Method != "GET" && r.Method != "HEAD" && !can {
				w.WriteHeader(http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

type ProjectController struct {
	ProjectService server.ProjectService
}

// SendTestNotification triggers sending a test notification to enabled messengers for this project.
func (c *ProjectController) SendTestNotification(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)

	// Respect project.Alert flag: if disabled, still return 204 without sending
	if !project.Alert {
		w.WriteHeader(http.StatusConflict)
		return
	}

	err := tasks.SendProjectTestAlerts(project, helpers.Store(r))
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *ProjectController) UpdateProject(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var body db.Project

	if !helpers.Bind(w, r, &body) {
		return
	}

	if body.ID != project.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}

	err := c.ProjectService.UpdateProject(body)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteProject removes a project from the database
func (c *ProjectController) DeleteProject(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)

	err := c.ProjectService.DeleteProject(project.ID)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	err = util.Config.ClearProjectTmpDir(project.ID)
	if err != nil {
		log.Error(err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// GetProject returns a project details
func GetProject(w http.ResponseWriter, r *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, helpers.GetFromContext(r, "project"))
}

func GetUserRole(w http.ResponseWriter, r *http.Request) {
	var result struct {
		Role        db.ProjectUserRole       `json:"role"`
		RoleName    string                   `json:"role_name"`
		Permissions db.ProjectUserPermission `json:"permissions"`
	}
	result.Role = helpers.GetFromContext(r, "projectUserRole").(db.ProjectUserRole)
	result.RoleName, _ = helpers.GetFromContext(r, "projectUserRoleName").(string)
	result.Permissions = helpers.GetFromContext(r, "permissions").(db.ProjectUserPermission)
	helpers.WriteJSON(w, http.StatusOK, result)
}

func ClearCache(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)

	err := util.Config.ClearProjectTmpDir(project.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
