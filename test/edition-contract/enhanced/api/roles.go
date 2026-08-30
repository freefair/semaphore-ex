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
	store              db.Store
	capabilityProvider pro_interfaces.CapabilityProvider
}

func NewRolesController(
	store db.Store,
	capabilityProvider pro_interfaces.CapabilityProvider,
) *RolesController {
	return &RolesController{store: store, capabilityProvider: capabilityProvider}
}

func (c *RolesController) GetGlobalPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	helpers.WriteJSON(w, http.StatusOK, pro_interfaces.GlobalPermissionCatalog())
}

func (c *RolesController) GetGlobalRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	roleID, ok := roleRequestID(w, r)
	if !ok {
		return
	}
	role, err := c.store.GetGlobalRoleByID(roleID)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, role)
}

func (c *RolesController) GetRoles(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	roles, err := c.store.GetGlobalRoles()
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	if roles == nil {
		roles = make([]db.Role, 0)
	}
	helpers.WriteJSON(w, http.StatusOK, roles)
}

func (c *RolesController) AddRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	var input struct {
		Name              string                   `json:"name"`
		Permissions       db.ProjectUserPermission `json:"permissions"`
		GlobalPermissions db.GlobalPermission      `json:"global_permissions"`
	}
	if !helpers.Bind(w, r, &input) {
		return
	}
	roleID, err := newProjectRoleID()
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	role, err := c.store.CreateGlobalRole(db.Role{
		ID: roleID, Slug: string(roleID), Name: input.Name,
		Permissions: input.Permissions, GlobalPermissions: input.GlobalPermissions,
		Revision: 1,
	})
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, role)
}

func (c *RolesController) UpdateRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	roleID, ok := roleRequestID(w, r)
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
	role.ProjectID = nil
	if role.Slug == "" {
		role.Slug = string(roleID)
	}
	updated, err := c.store.UpdateGlobalRole(role, role.Revision)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, updated)
}

func (c *RolesController) DeleteRole(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	roleID, ok := roleRequestID(w, r)
	if !ok {
		return
	}
	revision, ok := positiveQueryInt(w, r, "revision", "A positive role revision is required")
	if !ok {
		return
	}
	if err := c.store.DeleteGlobalRole(roleID, revision); err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *RolesController) GetGlobalRoleAssignments(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	userID, ok := globalRoleRequestUserID(w, r)
	if !ok || !c.requireExistingUser(w, userID) {
		return
	}
	assignments, err := c.store.GetGlobalRoleAssignments(userID)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	if assignments == nil {
		assignments = make([]db.GlobalRoleAssignment, 0)
	}
	helpers.WriteJSON(w, http.StatusOK, assignments)
}

func (c *RolesController) AddGlobalRoleAssignment(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	userID, ok := globalRoleRequestUserID(w, r)
	if !ok {
		return
	}
	var input struct {
		RoleID db.ProjectRoleID `json:"role_id"`
	}
	if !helpers.Bind(w, r, &input) {
		return
	}
	assignment, err := c.store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{
		UserID: userID, RoleID: input.RoleID, Revision: 1,
	})
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusCreated, assignment)
}

func (c *RolesController) DeleteGlobalRoleAssignment(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessWrite) {
		return
	}
	userID, ok := globalRoleRequestUserID(w, r)
	if !ok {
		return
	}
	assignmentID, err := strconv.Atoi(mux.Vars(r)["assignment_id"])
	if err != nil || assignmentID <= 0 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	revision, ok := positiveQueryInt(
		w, r, "revision", "A positive assignment revision is required",
	)
	if !ok {
		return
	}
	if err = c.store.DeleteGlobalRoleAssignment(userID, assignmentID, revision); err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *RolesController) GetEffectiveGlobalPermissions(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	userID, ok := globalRoleRequestUserID(w, r)
	if !ok {
		return
	}
	user, err := c.store.GetUser(userID)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	permissions, err := c.store.GetEffectiveGlobalPermissions(userID)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	assignments, err := c.store.GetGlobalRoleAssignments(userID)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, pro_interfaces.EffectiveGlobalPermissions{
		Permissions: permissions,
		Decisions: pro_interfaces.ExplainEffectiveGlobalPermissions(
			user.Admin, assignments,
		).Decisions,
	})
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
	roles, err := c.store.GetProjectRoles(project.ID)
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
	role, err := c.store.CreateProjectRole(db.Role{
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
	role, err := c.store.GetProjectRoleByID(project.ID, roleID)
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
	updated, err := c.store.UpdateProjectRole(project.ID, role, role.Revision)
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
	if err = c.store.DeleteProjectRole(project.ID, roleID, revision); err != nil {
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
	return roleRequestID(w, r)
}

func roleRequestID(w http.ResponseWriter, r *http.Request) (db.ProjectRoleID, bool) {
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

func globalRoleRequestUserID(w http.ResponseWriter, r *http.Request) (int, bool) {
	userID, err := strconv.Atoi(mux.Vars(r)["user_id"])
	if err != nil || userID <= 0 {
		w.WriteHeader(http.StatusNotFound)
		return 0, false
	}
	return userID, true
}

func positiveQueryInt(
	w http.ResponseWriter,
	r *http.Request,
	name string,
	message string,
) (int, bool) {
	value, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || value <= 0 {
		helpers.WriteErrorStatus(w, message, http.StatusBadRequest)
		return 0, false
	}
	return value, true
}

func (c *RolesController) requireExistingUser(w http.ResponseWriter, userID int) bool {
	if _, err := c.store.GetUser(userID); err != nil {
		writeGlobalRoleError(w, err)
		return false
	}
	return true
}

func writeGlobalRoleError(w http.ResponseWriter, err error) {
	var capabilityDenied pro_interfaces.CapabilityDeniedError
	switch {
	case errors.As(err, &capabilityDenied):
		w.WriteHeader(http.StatusForbidden)
	case errors.Is(err, db.ErrNotFound):
		w.WriteHeader(http.StatusNotFound)
	case errors.Is(err, db.ErrGlobalRoleRevisionConflict):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "GLOBAL_ROLE_REVISION_CONFLICT", "message": err.Error(),
		})
	case errors.Is(err, db.ErrGlobalRoleAssignmentConflict):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "GLOBAL_ROLE_ASSIGNMENT_REVISION_CONFLICT", "message": err.Error(),
		})
	case errors.Is(err, db.ErrGlobalRoleAssigned):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "GLOBAL_ROLE_ASSIGNED", "message": err.Error(),
		})
	case errors.Is(err, db.ErrLastGlobalAdministrator):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{
			"code": "LAST_GLOBAL_ADMINISTRATOR", "message": err.Error(),
		})
	default:
		helpers.WriteError(w, err)
	}
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
