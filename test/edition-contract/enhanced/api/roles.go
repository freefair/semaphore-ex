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
	"github.com/semaphoreui/semaphore/services/audit"
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

// GetGlobalProjectPermissionCatalog exposes the project permission definitions
// usable by a global role. Global roles are not tied to an arbitrary project,
// so this deliberately uses the authoritative project catalog directly.
func (c *RolesController) GetGlobalProjectPermissionCatalog(w http.ResponseWriter, r *http.Request) {
	if !c.requireCapability(w, r, pro_interfaces.CapabilityAccessRead) {
		return
	}
	helpers.WriteJSON(w, http.StatusOK, pro_interfaces.ProjectPermissionCatalog())
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
	c.recordGlobalRoleAudit(r, audit.IAMRoleCreate, role, nil)
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
	c.recordGlobalRoleAudit(r, audit.IAMRoleUpdate, updated, nil)
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
	role, err := c.store.GetGlobalRoleByID(roleID)
	if err != nil {
		writeGlobalRoleError(w, err)
		return
	}
	if r.URL.Query().Get("force") == "true" {
		impact, forceErr := c.store.ForceDeleteGlobalRole(roleID, revision)
		if forceErr != nil {
			writeGlobalRoleError(w, forceErr)
			return
		}
		c.recordGlobalRoleAudit(r, audit.IAMRoleDelete, role, &impact)
		helpers.WriteJSON(w, http.StatusOK, impact)
		return
	}
	if err := c.store.DeleteGlobalRole(roleID, revision); err != nil {
		if errors.Is(err, db.ErrGlobalRoleAssigned) {
			impact, impactErr := c.store.GetRoleAssignments(roleID)
			if impactErr == nil {
				helpers.WriteJSON(w, http.StatusConflict, map[string]any{"code": "GLOBAL_ROLE_ASSIGNED", "impact": impact.Impact()})
				return
			}
		}
		writeGlobalRoleError(w, err)
		return
	}
	c.recordGlobalRoleAudit(r, audit.IAMRoleDelete, role, nil)
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
	roles = append(roles, virtualBuiltinProjectRoles()...)
	helpers.WriteJSON(w, http.StatusOK, roles)
}

func virtualBuiltinProjectRoles() []db.Role {
	builtins := []db.ProjectUserRole{
		db.ProjectOwner,
		db.ProjectManager,
		db.ProjectTaskRunner,
		db.ProjectGuest,
	}
	roles := make([]db.Role, 0, len(builtins))
	for _, builtin := range builtins {
		roles = append(roles, db.Role{
			ID:          db.ProjectRoleID("builtin:" + string(builtin)),
			Slug:        string(builtin),
			Name:        builtinProjectRoleName(builtin),
			Permissions: builtin.GetPermissions(),
			Revision:    1,
			Builtin:     true,
		})
	}
	return roles
}

func builtinProjectRoleName(role db.ProjectUserRole) string {
	switch role {
	case db.ProjectOwner:
		return "Owner"
	case db.ProjectManager:
		return "Manager"
	case db.ProjectTaskRunner:
		return "Task Runner"
	case db.ProjectGuest:
		return "Guest"
	default:
		return string(role)
	}
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
	c.recordProjectRoleAudit(r, audit.IAMProjectRoleDefinitionCreate, role, nil)
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
	c.recordProjectRoleAudit(r, audit.IAMProjectRoleDefinitionUpdate, updated, nil)
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
	role, err := c.store.GetProjectRoleByID(project.ID, roleID)
	if err != nil {
		writeProjectRoleError(w, err)
		return
	}
	if r.URL.Query().Get("force") == "true" {
		impact, forceErr := c.store.ForceDeleteProjectRole(project.ID, roleID, revision)
		if forceErr != nil {
			writeProjectRoleError(w, forceErr)
			return
		}
		c.recordProjectRoleAudit(r, audit.IAMProjectRoleDefinitionDelete, role, &impact)
		helpers.WriteJSON(w, http.StatusOK, impact)
		return
	}
	if err = c.store.DeleteProjectRole(project.ID, roleID, revision); err != nil {
		if errors.Is(err, db.ErrProjectRoleAssigned) {
			impact, impactErr := c.store.GetRoleAssignments(roleID)
			if impactErr == nil {
				helpers.WriteJSON(w, http.StatusConflict, map[string]any{"code": "PROJECT_ROLE_ASSIGNED", "impact": impact.Impact()})
				return
			}
		}
		writeProjectRoleError(w, err)
		return
	}
	c.recordProjectRoleAudit(r, audit.IAMProjectRoleDefinitionDelete, role, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (c *RolesController) recordGlobalRoleAudit(
	r *http.Request,
	kind audit.Kind,
	role db.Role,
	impact *db.RoleImpact,
) {
	event := audit.Event{Kind: kind, Target: roleAuditTarget(audit.TargetRole, role)}
	if kind == audit.IAMRoleDelete {
		event.Metadata = audit.RoleDeleteMetadata{Slug: role.Slug, Impact: impact}
	} else {
		event.Metadata = audit.RoleMetadata{Slug: role.Slug, Permissions: roleAuditPermissionNames(role)}
	}
	helpers.Audit(r).Record(r.Context(), event)
}

func (c *RolesController) recordProjectRoleAudit(
	r *http.Request,
	kind audit.Kind,
	role db.Role,
	impact *db.RoleImpact,
) {
	event := audit.Event{
		Kind:      kind,
		Target:    roleAuditTarget(audit.TargetProjectRoleDefinition, role),
		ProjectID: projectIDFromRole(role),
	}
	if kind == audit.IAMProjectRoleDefinitionDelete {
		event.Metadata = audit.RoleDeleteMetadata{Slug: role.Slug, Impact: impact}
	} else {
		event.Metadata = audit.RoleMetadata{Slug: role.Slug, Permissions: roleAuditPermissionNames(role)}
	}
	helpers.Audit(r).Record(r.Context(), event)
}

func projectIDFromRole(role db.Role) int {
	if role.ProjectID == nil {
		return 0
	}
	return *role.ProjectID
}

func roleAuditTarget(targetType string, role db.Role) *audit.Target {
	return &audit.Target{
		Type: targetType,
		ID:   audit.TruncateName(string(role.ID), audit.MaxNameBytes),
		Name: audit.TruncateName(role.Name, audit.MaxNameBytes),
	}
}

func roleAuditPermissionNames(role db.Role) []string {
	permissions := make([]string, 0)
	for _, definition := range pro_interfaces.ProjectPermissionCatalog() {
		if role.Permissions.Can(definition.Permission) {
			permissions = append(permissions, string(definition.ID))
		}
	}
	for _, definition := range pro_interfaces.GlobalPermissionCatalog() {
		if role.GlobalPermissions.Can(db.GlobalPermission(definition.Permission)) {
			permissions = append(permissions, string(definition.ID))
		}
	}
	return permissions
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
	case errors.Is(err, db.ErrGlobalRoleForceProtected):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{"code": "GLOBAL_ROLE_FORCE_PROTECTED", "message": err.Error()})
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
	case errors.Is(err, db.ErrProjectRoleForceProtected):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{"code": "PROJECT_ROLE_FORCE_PROTECTED", "message": err.Error()})
	case errors.Is(err, db.ErrLastProjectAdministrator):
		helpers.WriteJSON(w, http.StatusConflict, map[string]string{"code": "LAST_PROJECT_ADMINISTRATOR", "message": err.Error()})
	default:
		helpers.WriteError(w, err)
	}
}
