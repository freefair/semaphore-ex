package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

type RolesController struct {
	roleRepo           db.RoleRepository
	capabilityProvider pro_interfaces.CapabilityProvider
}

func NewRolesController(
	roleRepo db.RoleRepository,
	capabilityProvider pro_interfaces.CapabilityProvider,
) *RolesController {
	return &RolesController{roleRepo: roleRepo, capabilityProvider: capabilityProvider}
}

func (c *RolesController) GetGlobalRole(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) GetRoles(w http.ResponseWriter, _ *http.Request) {
	helpers.WriteJSON(w, http.StatusOK, []db.Role{})
}

func (c *RolesController) AddRole(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) UpdateRole(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) DeleteRole(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNotFound)
}

func (c *RolesController) GetProjectPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	helpers.WriteJSON(w, http.StatusOK, pro_interfaces.ProjectPermissionCatalog())
}

func (c *RolesController) GetProjectRoles(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	project, ok := projectRoleRequestProject(w, r)
	if !ok {
		return
	}
	roles, err := c.roleRepo.GetProjectRoles(project.ID)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if roles == nil {
		roles = make([]db.Role, 0)
	}
	helpers.WriteJSON(w, http.StatusOK, roles)
}

func (c *RolesController) GetProjectAndGlobalRoles(w http.ResponseWriter, r *http.Request) {
	// Global roles belong to Slice 046. Keeping this project-only prevents a
	// project administrator from assigning a role outside the current scope.
	c.GetProjectRoles(w, r)
}

func (c *RolesController) AddProjectRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	project, ok := projectRoleRequestProject(w, r)
	if !ok {
		return
	}
	var input struct {
		Name        string                   `json:"name"`
		Permissions db.ProjectUserPermission `json:"permissions"`
	}
	if !helpers.Bind(w, r, &input) {
		return
	}
	roleID, err := newProjectRoleID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	role, err := c.roleRepo.CreateProjectRole(db.Role{
		ID: roleID, Slug: string(roleID), Name: input.Name,
		Permissions: input.Permissions, ProjectID: &project.ID, Revision: 1,
	})
	if err != nil {
		writeProjectRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, role)
}

func (c *RolesController) GetProjectRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	project, ok := projectRoleRequestProject(w, r)
	if !ok {
		return
	}
	roleID, ok := projectRoleRequestID(w, r)
	if !ok {
		return
	}
	role, err := c.roleRepo.GetProjectRoleByID(project.ID, roleID)
	if err != nil {
		writeProjectRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, role)
}

func (c *RolesController) UpdateProjectRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	project, ok := projectRoleRequestProject(w, r)
	if !ok {
		return
	}
	roleID, ok := projectRoleRequestID(w, r)
	if !ok {
		return
	}
	var role db.Role
	if !helpers.Bind(w, r, &role) {
		return
	}
	if role.ID != "" && role.ID != roleID {
		helpers.WriteErrorStatus(w, "Role ID cannot be changed", http.StatusBadRequest)
		return
	}
	role.ID = roleID
	role.ProjectID = &project.ID
	updated, err := c.roleRepo.UpdateProjectRole(project.ID, role, role.Revision)
	if err != nil {
		writeProjectRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *RolesController) DeleteProjectRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	project, ok := projectRoleRequestProject(w, r)
	if !ok {
		return
	}
	roleID, ok := projectRoleRequestID(w, r)
	if !ok {
		return
	}
	revision, err := strconv.Atoi(r.URL.Query().Get("revision"))
	if err != nil || revision <= 0 {
		helpers.WriteErrorStatus(w, "A positive role revision is required", http.StatusBadRequest)
		return
	}
	if err = c.roleRepo.DeleteProjectRole(project.ID, roleID, revision); err != nil {
		writeProjectRoleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *RolesController) requireCapability(
	w http.ResponseWriter,
	r *http.Request,
	access pro_interfaces.CapabilityAccess,
) bool {
	user, ok := helpers.GetFromContext(r, "user").(*db.User)
	if !ok || user == nil || c.capabilityProvider == nil {
		w.WriteHeader(http.StatusForbidden)
		return false
	}
	snapshot, err := c.capabilityProvider.Resolve(r.Context(), pro_interfaces.CapabilityRequest{
		UserID: user.ID, IsAdmin: user.Admin, At: time.Now().UTC(),
	})
	if err == nil {
		err = snapshot.Require(pro_interfaces.CapabilityProjectRoles, access)
	}
	if err != nil {
		writeProjectRoleError(w, err)
		return false
	}
	return true
}

func projectRoleRequestProject(w http.ResponseWriter, r *http.Request) (db.Project, bool) {
	project, ok := helpers.GetFromContext(r, "project").(db.Project)
	if !ok || project.ID <= 0 {
		w.WriteHeader(http.StatusNotFound)
		return db.Project{}, false
	}
	return project, true
}

func projectRoleRequestID(w http.ResponseWriter, r *http.Request) (db.ProjectRoleID, bool) {
	value := mux.Vars(r)["role_id"]
	if value == "" {
		value = mux.Vars(r)["role_slug"]
	}
	value = strings.TrimSpace(value)
	if value == "" {
		w.WriteHeader(http.StatusNotFound)
		return "", false
	}
	return db.ProjectRoleID(value), true
}

func newProjectRoleID() (db.ProjectRoleID, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return db.ProjectRoleID("role_" + hex.EncodeToString(random)), nil
}

func writeProjectRoleError(w http.ResponseWriter, err error) {
	var capabilityDenied pro_interfaces.CapabilityDeniedError
	switch {
	case errors.As(err, &capabilityDenied):
		w.WriteHeader(http.StatusForbidden)
	case errors.Is(err, db.ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, db.ErrProjectRoleRevisionConflict):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "PROJECT_ROLE_REVISION_CONFLICT", "message": err.Error(),
		})
	case errors.Is(err, db.ErrProjectRoleAssigned):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "PROJECT_ROLE_ASSIGNED", "message": err.Error(),
		})
	default:
		helpers.WriteError(w, err)
	}
}
