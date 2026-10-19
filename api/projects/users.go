package projects

import (
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/services/audit"
	"net/http"
	"slices"
	"strings"
)

// UserMiddleware ensures a user exists and loads it to the context
func UserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		userID, ok := helpers.GetIntParamOrAbort("user_id", w, r)
		if !ok {
			return
		}

		memberRoles, err := helpers.Store(r).GetProjectUserRoles(project.ID, userID)
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		if len(memberRoles) == 0 {
			helpers.WriteError(w, db.ErrNotFound)
			return
		}

		user, err := helpers.Store(r).GetUser(userID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		r = helpers.SetContextValue(r, "projectUser", user)
		r = helpers.SetContextValue(r, "projectMemberRoles", memberRoles)
		next.ServeHTTP(w, r)
	})
}

type projUser struct {
	ID                   int                      `json:"id"`
	Username             string                   `json:"username"`
	Name                 string                   `json:"name"`
	Role                 string                   `json:"role"`
	Roles                []string                 `json:"roles"`
	RoleID               *db.ProjectRoleID        `json:"role_id,omitempty"`
	Revision             int                      `json:"revision"`
	EffectivePermissions db.ProjectUserPermission `json:"effective_permissions"`
	MappedRoles          []mappedRole             `json:"mapped_roles,omitempty"`
}

// mappedRole exposes directory provenance for roles excluded from the mutable
// plural roles list.
type mappedRole struct {
	Provider string `json:"provider"`
	Group    string `json:"group"`
	Role     string `json:"role"`
}

// takesLastOwner reports whether the change takes the owner role from the only owner of the project.
func takesLastOwner(store db.Store, projectID int, oldRoles []db.Role, newRoles []db.Role) (bool, error) {
	isOwner := func(role db.Role) bool { return role.Builtin && role.Slug == string(db.ProjectOwner) }
	if !slices.ContainsFunc(oldRoles, isOwner) || slices.ContainsFunc(newRoles, isOwner) {
		return false, nil
	}
	owners, err := store.CountProjectOwners(projectID)
	return owners == 1, err
}

// resolveRoles accepts one legacy role reference or upstream's plural role
// references. Singular input keeps immutable-ID compatibility; plural input
// resolves scoped slugs first so response values round-trip without ambiguity.
func resolveRoles(store db.Store, projectID int, role string, roles []string) ([]db.Role, error) {
	refs := roles
	preferSlug := true
	if role != "" {
		if len(roles) > 0 {
			return nil, &common_errors.ValidationError{Message: "Send either role or roles, not both"}
		}
		refs = []string{role}
		preferSlug = false
	}
	if len(refs) == 0 {
		return nil, &common_errors.ValidationError{Message: "At least one role is required"}
	}
	result := make([]db.Role, 0, len(refs))
	for _, ref := range refs {
		found, err := resolveRoleReference(store, projectID, ref, preferSlug)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(result, func(r db.Role) bool { return r.ID == found.ID }) {
			result = append(result, found)
		}
	}
	return result, nil
}

func resolveRoleReference(store db.Store, projectID int, ref string, preferSlug bool) (db.Role, error) {
	// Built-in names are reserved case-insensitively. Resolve them before
	// considering custom immutable IDs, which may be historical collisions.
	if builtin := db.ProjectUserRole(strings.ToLower(ref)); builtin.IsValid() {
		return store.GetProjectOrGlobalRoleBySlug(projectID, string(builtin))
	}

	bySlug := func() (db.Role, error) {
		return store.GetProjectOrGlobalRoleBySlug(projectID, ref)
	}
	byID := func() (db.Role, error) {
		found, err := store.GetProjectRoleByID(projectID, db.ProjectRoleID(ref))
		if errors.Is(err, db.ErrNotFound) {
			found, err = store.GetGlobalRoleByID(db.ProjectRoleID(ref))
		}
		return found, err
	}

	first, second := byID, bySlug
	if preferSlug {
		first, second = bySlug, byID
	}
	found, err := first()
	if errors.Is(err, db.ErrNotFound) {
		found, err = second()
	}
	if errors.Is(err, db.ErrNotFound) {
		return db.Role{}, &common_errors.ValidationError{Message: "Role does not exist in this project"}
	}
	if err != nil {
		return db.Role{}, err
	}
	return found, nil
}

func roleIDs(roles []db.Role) []db.ProjectRoleID {
	ids := make([]db.ProjectRoleID, 0, len(roles))
	for _, role := range roles {
		ids = append(ids, role.ID)
	}
	return ids
}

// GetUsers returns all users in a project
func GetUsers(w http.ResponseWriter, r *http.Request) {
	// get single user if user ID specified in the request
	if user := helpers.GetFromContext(r, "projectUser"); user != nil {
		helpers.WriteJSON(w, http.StatusOK, user.(db.User))
		return
	}

	project := helpers.GetFromContext(r, "project").(db.Project)
	users, err := helpers.Store(r).GetProjectUsers(project.ID, helpers.QueryParams(r.URL))

	if err != nil {
		writeProjectMembershipError(w, err)
		return
	}

	result := make([]projUser, 0)

	for _, user := range users {
		membership, membershipErr := helpers.Store(r).GetProjectUser(project.ID, user.ID)
		if membershipErr != nil {
			helpers.WriteError(w, membershipErr)
			return
		}
		member, memberErr := helpers.Store(r).GetProjectMember(project.ID, user.ID)
		if memberErr != nil {
			helpers.WriteError(w, memberErr)
			return
		}
		item := projUser{ID: user.ID, Name: user.Name, Username: user.Username, Roles: make([]string, 0, len(member.Roles)), Revision: membership.Revision}
		for _, role := range member.Roles {
			item.Roles = append(item.Roles, role.Slug)
		}
		for _, role := range user.Roles {
			item.EffectivePermissions |= role.Permissions
		}
		for _, role := range member.MappedRoles {
			item.MappedRoles = append(item.MappedRoles, mappedRole{Provider: role.Provider, Group: role.Group, Role: role.RoleSlug})
		}
		// One value would hide the other roles, so old clients see it only for a single role.
		if len(user.Roles) == 1 {
			role := user.Roles[0]
			item.Role = role.Slug
			if !role.Builtin {
				item.Role = string(role.ID)
				item.RoleID = &role.ID
			}
		}
		result = append(result, item)
	}

	helpers.WriteJSON(w, http.StatusOK, result)
}

// AddUser adds a user to a projects team in the database
func AddUser(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	store := helpers.Store(r)
	var projectUser struct {
		UserID   int      `json:"user_id" binding:"required"`
		Role     string   `json:"role"`
		Roles    []string `json:"roles"`
		Revision int      `json:"revision"`
	}

	if !helpers.Bind(w, r, &projectUser) {
		return
	}

	roles, err := resolveRoles(store, project.ID, projectUser.Role, projectUser.Roles)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	_, err = store.CreateProjectUserWithRoleRefs(
		db.ProjectUser{ProjectID: project.ID, UserID: projectUser.UserID, Revision: 1},
		roleIDs(roles),
	)
	if err != nil {
		writeProjectMembershipError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogCreate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   project.ID,
		ObjectType:  db.EventUser,
		ObjectID:    projectUser.UserID,
		Description: fmt.Sprintf("User ID %d added to team", projectUser.UserID),
	})

	target := audit.UserTarget(projectUser.UserID, "")
	if added, err := helpers.Store(r).GetUser(projectUser.UserID); err == nil {
		target.Name = added.Username
	}
	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:      audit.IAMMembershipAdd,
		Target:    target,
		ProjectID: project.ID,
		Metadata:  audit.MembershipMetadata{Roles: audit.NewRoleRefs(roles)},
	})

	w.WriteHeader(http.StatusNoContent)
}

// removeUser removes a user from a project team
func removeUser(targetUser db.User, roles []db.Role, w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	me := helpers.GetFromContext(r, "user").(*db.User) // logged in user

	event := audit.Event{
		Kind:      audit.IAMMembershipRemove,
		Target:    audit.UserTarget(targetUser.ID, targetUser.Username),
		ProjectID: project.ID,
		Metadata:  audit.MembershipMetadata{Roles: audit.NewRoleRefs(roles), SelfRemoval: targetUser.ID == me.ID},
	}

	if !me.Admin {
		last, err := takesLastOwner(helpers.Store(r), project.ID, roles, nil)
		if err != nil {
			helpers.WriteError(w, err)
			return
		}
		if last {
			event.Outcome = audit.OutcomeFailure
			event.Reason = audit.ReasonLastOwner
			helpers.Audit(r).Record(r.Context(), event)
			helpers.WriteError(w, &common_errors.ValidationError{Message: "The project needs at least one Owner"})
			return
		}
	}

	err := helpers.Store(r).DeleteProjectUser(project.ID, targetUser.ID)
	if errors.Is(err, db.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err != nil {
		writeProjectMembershipError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogDelete, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   project.ID,
		ObjectType:  db.EventUser,
		ObjectID:    targetUser.ID,
		Description: fmt.Sprintf("User ID %d removed from team", targetUser.ID),
	})
	helpers.Audit(r).Record(r.Context(), event)

	w.WriteHeader(http.StatusNoContent)
}

// LeftProject removes a user from a project team
func LeftProject(w http.ResponseWriter, r *http.Request) {
	me := helpers.GetFromContext(r, "user").(*db.User) // logged in user
	roles, _ := helpers.GetFromContext(r, "projectRoles").([]db.Role)
	removeUser(*me, roles, w, r)
}

// RemoveUser removes a user from a project team
func RemoveUser(w http.ResponseWriter, r *http.Request) {
	targetUser := helpers.GetFromContext(r, "projectUser").(db.User) // target user
	roles, _ := helpers.GetFromContext(r, "projectMemberRoles").([]db.Role)
	removeUser(targetUser, roles, w, r)
}

func UpdateUser(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	store := helpers.Store(r)
	targetUser := helpers.GetFromContext(r, "projectUser").(db.User)
	oldRoles, _ := helpers.GetFromContext(r, "projectMemberRoles").([]db.Role)

	event := audit.Event{
		Kind:      audit.IAMProjectRoleChange,
		Target:    audit.UserTarget(targetUser.ID, targetUser.Username),
		ProjectID: project.ID,
		Metadata:  audit.ProjectRoleMetadata{OldRoles: audit.NewRoleRefs(oldRoles)},
	}

	var projectUser struct {
		Role     string   `json:"role"`
		Roles    []string `json:"roles"`
		Revision int      `json:"revision"`
	}

	if !helpers.Bind(w, r, &projectUser) {
		return
	}
	if projectUser.Revision <= 0 {
		helpers.WriteErrorStatus(w, "A positive membership revision is required", http.StatusBadRequest)
		return
	}

	newRoles, err := resolveRoles(store, project.ID, projectUser.Role, projectUser.Roles)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	last, err := takesLastOwner(store, project.ID, oldRoles, newRoles)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if last {
		event.Outcome = audit.OutcomeFailure
		event.Reason = audit.ReasonLastOwner
		helpers.Audit(r).Record(r.Context(), event)
		helpers.WriteError(w, &common_errors.ValidationError{Message: "The project needs at least one Owner"})
		return
	}
	err = store.SetProjectUserRoleRefs(project.ID, targetUser.ID, roleIDs(newRoles), projectUser.Revision)

	if err != nil {
		writeProjectMembershipError(w, err)
		return
	}

	helpers.EventLog(r, helpers.EventLogUpdate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   project.ID,
		ObjectType:  db.EventUser,
		ObjectID:    targetUser.ID,
		Description: fmt.Sprintf("Changed role for User ID %d", targetUser.ID),
	})
	event.Metadata = audit.ProjectRoleMetadata{
		OldRoles: audit.NewRoleRefs(oldRoles),
		NewRoles: audit.NewRoleRefs(newRoles),
	}
	helpers.Audit(r).Record(r.Context(), event)

	w.WriteHeader(http.StatusNoContent)
}

func projectMembershipRoleAuditName(r *http.Request, projectID int, membership db.ProjectUser) string {
	if membership.RoleID != nil {
		if role, err := helpers.Store(r).GetProjectRoleByID(projectID, *membership.RoleID); err == nil {
			return audit.TruncateName(role.Slug, audit.MaxNameBytes)
		}
	}
	return audit.TruncateName(string(membership.Role), audit.MaxNameBytes)
}
