package projects

import (
	"fmt"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

// UserMiddleware ensures a user exists and loads it to the context
func UserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		project := helpers.GetFromContext(r, "project").(db.Project)
		userID, ok := helpers.GetIntParamOrAbort("user_id", w, r)
		if !ok {
			return
		}

		_, err := helpers.Store(r).GetProjectUser(project.ID, userID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		user, err := helpers.Store(r).GetUser(userID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		r = helpers.SetContextValue(r, "projectUser", user)
		next.ServeHTTP(w, r)
	})
}

type projUser struct {
	ID                   int                      `json:"id"`
	Username             string                   `json:"username"`
	Name                 string                   `json:"name"`
	Role                 db.ProjectUserRole       `json:"role"`
	RoleID               *db.ProjectRoleID        `json:"role_id,omitempty"`
	Revision             int                      `json:"revision"`
	EffectivePermissions db.ProjectUserPermission `json:"effective_permissions"`
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
		helpers.WriteError(w, err)
		return
	}

	var result = make([]projUser, 0)

	for _, user := range users {
		role := user.Role
		permissions := role.GetPermissions()
		if user.RoleID != nil {
			customRole, roleErr := helpers.Store(r).GetProjectRoleByID(project.ID, *user.RoleID)
			if roleErr != nil {
				helpers.WriteError(w, roleErr)
				return
			}
			role = db.ProjectUserRole(customRole.ID)
			permissions = customRole.Permissions
		}
		result = append(result, projUser{
			ID:                   user.ID,
			Name:                 user.Name,
			Username:             user.Username,
			Role:                 role,
			RoleID:               user.RoleID,
			Revision:             user.Revision,
			EffectivePermissions: permissions,
		})
	}

	helpers.WriteJSON(w, http.StatusOK, result)
}

// AddUser adds a user to a projects team in the database
func AddUser(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	var projectUser struct {
		UserID   int                `json:"user_id" binding:"required"`
		Role     db.ProjectUserRole `json:"role"`
		Revision int                `json:"revision"`
	}

	if !helpers.Bind(w, r, &projectUser) {
		return
	}

	role, roleID, ok := resolveProjectMembershipRole(w, r, project.ID, projectUser.Role)
	if !ok {
		return
	}

	_, err := helpers.Store(r).CreateProjectUser(db.ProjectUser{
		ProjectID: project.ID,
		UserID:    projectUser.UserID,
		Role:      role,
		RoleID:    roleID,
		Revision:  projectUser.Revision,
	})

	if err != nil {
		w.WriteHeader(http.StatusConflict)
		return
	}

	helpers.EventLog(r, helpers.EventLogCreate, helpers.EventLogItem{
		UserID:      helpers.UserFromContext(r).ID,
		ProjectID:   project.ID,
		ObjectType:  db.EventUser,
		ObjectID:    projectUser.UserID,
		Description: fmt.Sprintf("User ID %d added to team", projectUser.UserID),
	})

	w.WriteHeader(http.StatusNoContent)
}

// removeUser removes a user from a project team
func removeUser(targetUser db.User, w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	me := helpers.GetFromContext(r, "user").(*db.User) // logged in user
	myRole := helpers.GetFromContext(r, "projectUserRole").(db.ProjectUserRole)

	if !me.Admin && targetUser.ID == me.ID && myRole == db.ProjectOwner {
		helpers.WriteError(w, fmt.Errorf("owner can not left the project"))
		return
	}

	err := helpers.Store(r).DeleteProjectUser(project.ID, targetUser.ID)

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

	w.WriteHeader(http.StatusNoContent)
}

// LeftProject removes a user from a project team
func LeftProject(w http.ResponseWriter, r *http.Request) {
	me := helpers.GetFromContext(r, "user").(*db.User) // logged in user
	removeUser(*me, w, r)
}

// RemoveUser removes a user from a project team
func RemoveUser(w http.ResponseWriter, r *http.Request) {
	targetUser := helpers.GetFromContext(r, "projectUser").(db.User) // target user
	removeUser(targetUser, w, r)
}

func UpdateUser(w http.ResponseWriter, r *http.Request) {
	project := helpers.GetFromContext(r, "project").(db.Project)
	me := helpers.GetFromContext(r, "user").(*db.User) // logged in user
	targetUser := helpers.GetFromContext(r, "projectUser").(db.User)
	targetUserRole := helpers.GetFromContext(r, "projectUserRole").(db.ProjectUserRole)

	if !me.Admin && targetUser.ID == me.ID && targetUserRole == db.ProjectOwner {
		helpers.WriteError(w, fmt.Errorf("owner can not change his role in the project"))
		return
	}

	var projectUser struct {
		Role     db.ProjectUserRole `json:"role"`
		Revision int                `json:"revision"`
	}

	if !helpers.Bind(w, r, &projectUser) {
		return
	}
	if projectUser.Revision <= 0 {
		helpers.WriteErrorStatus(w, "A positive membership revision is required", http.StatusBadRequest)
		return
	}

	role, roleID, ok := resolveProjectMembershipRole(w, r, project.ID, projectUser.Role)
	if !ok {
		return
	}

	err := helpers.Store(r).UpdateProjectUser(db.ProjectUser{
		UserID:    targetUser.ID,
		ProjectID: project.ID,
		Role:      role,
		RoleID:    roleID,
		Revision:  projectUser.Revision,
	})

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

	w.WriteHeader(http.StatusNoContent)
}
