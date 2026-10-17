package projects

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/audit"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"net/http"
	"strings"
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
		roles, err := helpers.Store(r).GetProjectUserRoles(projectID, user.ID)
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		if len(roles) == 0 && !user.Admin {
			helpers.WriteError(w, db.ErrNotFound)
			return
		}

		project, err := helpers.Store(r).GetProject(projectID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		var permissions db.ProjectUserPermission
		for _, role := range roles {
			permissions |= role.Permissions
		}

		basePermissions := permissions
		if helpers.HasParam("template_id", r) {
			templateID, templateOk := helpers.GetIntParamOrAbort("template_id", w, r)
			if !templateOk {
				return
			}
			grants, err := helpers.Store(r).GetTemplatePermission(project.ID, templateID, user.ID)
			if err != nil {
				helpers.WriteError(w, err)
				return
			}
			permissions = applyEffectiveTemplatePermissions(permissions, grants)
		}

		r = helpers.SetContextValue(r, "projectRoles", roles)
		r = helpers.SetContextValue(r, "basePermissions", basePermissions)
		r = helpers.SetContextValue(r, "permissions", permissions)
		r = helpers.SetContextValue(r, "project", project)
		next.ServeHTTP(w, r)
	})
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
				projectID := 0
				if project, ok := helpers.GetOkFromContext(r, "project"); ok {
					projectID = project.(db.Project).ID
				}
				// Every call site passes a single permission bit.
				helpers.RecordDenied(r, strings.Join(audit.PermissionNames(permissions), ","), projectID)
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
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		helpers.WriteErrorStatus(w, "Invalid format", http.StatusBadRequest)
		return
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		helpers.WriteErrorStatus(w, "Invalid format", http.StatusBadRequest)
		return
	}
	var body db.Project
	if err = json.Unmarshal(encoded, &body); err != nil {
		helpers.WriteErrorStatus(w, "Invalid format", http.StatusBadRequest)
		return
	}
	if _, present := payload["default_ssh_keys"]; !present {
		body.DefaultSSHKeys = project.DefaultSSHKeys
	}
	if _, present := payload["always_ssh_keys"]; !present {
		body.AlwaysSSHKeys = project.AlwaysSSHKeys
	}

	if body.ID != project.ID {
		helpers.WriteJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Project ID in body and URL must be the same",
		})
		return
	}
	if err := validateSSHKeyBindingsForProject(helpers.Store(r), project.ID, body.DefaultSSHKeys); err != nil {
		helpers.WriteError(w, err)
		return
	}
	if err := validateSSHKeyBindingsForProject(helpers.Store(r), project.ID, body.AlwaysSSHKeys); err != nil {
		helpers.WriteError(w, err)
		return
	}
	if _, err := db.ResolveTaskSSHKeys(body.DefaultSSHKeys, body.AlwaysSSHKeys, nil, nil); err != nil {
		helpers.WriteError(w, err)
		return
	}
	templates, err := helpers.Store(r).GetTemplates(project.ID, db.TemplateFilter{}, db.RetrieveQueryParams{})
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	for _, template := range templates {
		if _, err = db.ResolveTaskSSHKeys(body.DefaultSSHKeys, body.AlwaysSSHKeys, template.SSHKeys, nil); err != nil {
			helpers.WriteErrorStatus(w, "SSH key bindings conflict with template "+template.Name, http.StatusBadRequest)
			return
		}
	}

	err = c.ProjectService.UpdateProject(body)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.ResourceProjectUpdate,
		Target:    audit.ResourceTarget(audit.TargetProject, project.ID, body.Name),
		ProjectID: project.ID,
	})

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

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.ResourceProjectDelete,
		Target:    audit.ResourceTarget(audit.TargetProject, project.ID, project.Name),
		ProjectID: project.ID,
	})

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
	result := struct {
		Role        string                   `json:"role"`
		Roles       []string                 `json:"roles"`
		RoleName    string                   `json:"role_name"`
		RoleID      *db.ProjectRoleID        `json:"role_id,omitempty"`
		Permissions db.ProjectUserPermission `json:"permissions"`
	}{Roles: []string{}}
	roles := helpers.GetFromContext(r, "projectRoles").([]db.Role)
	for _, role := range roles {
		result.Roles = append(result.Roles, role.Slug)
	}
	if len(result.Roles) == 1 {
		role := roles[0]
		result.Role = role.Slug
		result.RoleName = role.Name
		if !role.Builtin {
			result.Role = string(role.ID)
			result.RoleID = &role.ID
		}
	}
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
