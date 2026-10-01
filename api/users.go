package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/audit"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

type UsersController struct {
	log *log.Entry
}

func NewUsersController() *UsersController {
	return &UsersController{
		log: log.WithField("context", "api.users"),
	}
}

type minimalUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

func (c *UsersController) GetUsers(w http.ResponseWriter, r *http.Request) {
	currentUser := helpers.GetFromContext(r, "user").(*db.User)
	canManageUsers, permissionErr := hasGlobalPermission(r, currentUser, db.CanManageGlobalUsers)
	if permissionErr != nil {
		helpers.WriteError(w, permissionErr)
		return
	}
	users, err := helpers.Store(r).GetUsers(db.RetrieveQueryParams{
		Filter: r.URL.Query().Get("s"),
	})

	if err != nil {
		panic(err)
	}

	if canManageUsers {
		helpers.WriteJSON(w, http.StatusOK, users)
	} else {
		var result = make([]minimalUser, 0)

		for _, user := range users {
			result = append(result, minimalUser{
				ID:       user.ID,
				Name:     user.Name,
				Username: user.Username,
			})
		}

		helpers.WriteJSON(w, http.StatusOK, result)
	}
}

func (c *UsersController) AddUser(w http.ResponseWriter, r *http.Request) {
	var user db.UserWithPwd
	if !helpers.Bind(w, r, &user) {
		return
	}
	// Commercial user entitlements are not part of the clean-room product.
	user.Pro = false

	editor := helpers.GetFromContext(r, "user").(*db.User)
	canManageUsers, err := hasGlobalPermission(r, editor, db.CanManageGlobalUsers)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	if !canManageUsers {
		c.log.WithField("editor", editor.Username).Debug("Not permitted to create users")
		helpers.RecordDenied(r, "admin", 0)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if user.Admin && !editor.Admin {
		c.log.WithField("editor", editor.Username).Debug("Delegated user manager cannot grant break-glass administration")
		helpers.RecordDenied(r, "admin", 0)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var createErr error
	var newUser db.User

	if user.External {
		newUser, createErr = helpers.Store(r).CreateUserWithoutPassword(user.User)
	} else {
		newUser, createErr = helpers.Store(r).CreateUser(user)
	}

	if createErr != nil {
		c.log.WithError(createErr).WithField("username", user.Username).Error("Failed to create user")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:     audit.IAMUserCreate,
		Target:   audit.UserTarget(newUser.ID, newUser.Username),
		Metadata: audit.UserCreateMetadata{Admin: newUser.Admin, Pro: newUser.Pro, External: newUser.External},
	})

	helpers.WriteJSON(w, http.StatusCreated, newUser)
}
func (c *UsersController) ReadonlyUserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := helpers.GetIntParamOrAbort("user_id", w, r)

		if !ok {
			return
		}

		user, err := helpers.Store(r).GetUser(userID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		editor := helpers.GetFromContext(r, "user").(*db.User)

		canManageUsers, permissionErr := hasGlobalPermission(r, editor, db.CanManageGlobalUsers)
		if permissionErr != nil {
			helpers.WriteError(w, permissionErr)
			return
		}
		if !canManageUsers && editor.ID != user.ID {
			user = db.User{
				ID:       user.ID,
				Username: user.Username,
				Name:     user.Name,
			}
		}

		r = helpers.SetContextValue(r, "_user", user)
		next.ServeHTTP(w, r)
	})
}

func (c *UsersController) GetUserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := helpers.GetIntParamOrAbort("user_id", w, r)

		if !ok {
			return
		}

		user, err := helpers.Store(r).GetUser(userID)

		if err != nil {
			helpers.WriteError(w, err)
			return
		}

		editor := helpers.GetFromContext(r, "user").(*db.User)

		canManageUsers, permissionErr := hasGlobalPermission(r, editor, db.CanManageGlobalUsers)
		if permissionErr != nil {
			helpers.WriteError(w, permissionErr)
			return
		}
		if !canManageUsers && editor.ID != user.ID {
			c.log.WithFields(log.Fields{
				"editor":  editor.Username,
				"user_id": user.ID,
			}).Debug("Not permitted to access another user")
			// Reads of another user are not audited, attempts to change them are.
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				helpers.RecordDenied(r, "admin", 0)
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		r = helpers.SetContextValue(r, "_user", user)
		next.ServeHTTP(w, r)
	})
}

func (c *UsersController) UpdateUser(w http.ResponseWriter, r *http.Request) {
	targetUser := helpers.GetFromContext(r, "_user").(db.User)
	editor := helpers.GetFromContext(r, "user").(*db.User)
	canManageUsers, permissionErr := hasGlobalPermission(r, editor, db.CanManageGlobalUsers)
	if permissionErr != nil {
		helpers.WriteError(w, permissionErr)
		return
	}

	var user db.UserWithPwd
	if !helpers.Bind(w, r, &user) {
		return
	}

	if !canManageUsers && editor.ID != targetUser.ID {
		c.log.WithFields(log.Fields{
			"editor":  editor.Username,
			"user_id": targetUser.ID,
		}).Debug("Not permitted to update another user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if targetUser.Admin && !editor.Admin {
		c.log.WithFields(log.Fields{
			"editor":  editor.Username,
			"user_id": targetUser.ID,
		}).Debug("Delegated user manager cannot modify built-in administrator")
		helpers.RecordDenied(r, "admin", 0)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if targetUser.Admin != user.Admin && !editor.Admin {
		c.log.WithField("editor", editor.Username).Debug("Not permitted to change own admin status")
		helpers.RecordDenied(r, "admin", 0)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	// Preserve the schema field while preventing legacy commercial state from
	// influencing authorization or feature availability.
	user.Pro = false

	if targetUser.External && targetUser.Username != user.Username {
		c.log.WithField("user_id", targetUser.ID).Debug("Username is not editable for external users")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	meta := userUpdateMetadata(targetUser, user)
	user.ID = targetUser.ID
	if err := helpers.Store(r).UpdateUser(user); err != nil {
		c.log.WithError(err).WithField("user_id", targetUser.ID).Error("Failed to update user")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:     audit.IAMUserUpdate,
		Target:   audit.UserTarget(targetUser.ID, user.Username),
		Metadata: meta,
	})
	if user.Pwd != "" {
		passwordKind := audit.IAMUserPasswordAdminReset
		if editor.ID == targetUser.ID {
			passwordKind = audit.IAMUserPasswordChange
		}
		helpers.Audit(r).Record(r.Context(), audit.Event{Kind: passwordKind, Target: audit.UserTarget(targetUser.ID, user.Username)})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *UsersController) UpdateUserPassword(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "_user").(db.User)
	editor := helpers.GetFromContext(r, "user").(*db.User)
	canManageUsers, permissionErr := hasGlobalPermission(r, editor, db.CanManageGlobalUsers)
	if permissionErr != nil {
		helpers.WriteError(w, permissionErr)
		return
	}

	var pwd struct {
		Pwd        string `json:"password"`
		CurrentPwd string `json:"current_password"`
	}

	if !canManageUsers && editor.ID != user.ID {
		c.log.WithFields(log.Fields{
			"editor":  editor.Username,
			"user_id": user.ID,
		}).Debug("Not permitted to change another user's password")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if user.Admin && !editor.Admin {
		c.log.WithFields(log.Fields{
			"editor":  editor.Username,
			"user_id": user.ID,
		}).Debug("Delegated user manager cannot reset built-in administrator password")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if user.External {
		c.log.WithField("user_id", user.ID).Debug("Password is not editable for external users")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !helpers.Bind(w, r, &pwd) {
		return
	}

	// CWE-620: require the current password when a user changes their own,
	// so a stolen session can't be used to take over the account. Admins
	// changing someone else's password are exempt — they can't know it.
	if editor.ID == user.ID {
		if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(pwd.CurrentPwd)); err != nil {
			c.log.WithField("user_id", user.ID).Debug("Current password does not match")
			helpers.Audit(r).Record(r.Context(), audit.Event{
				Kind:    audit.IAMUserPasswordChange,
				Outcome: audit.OutcomeFailure,
				Reason:  audit.ReasonInvalidCurrentPassword,
				Target:  audit.UserTarget(user.ID, user.Username),
			})
			helpers.WriteErrorStatus(w, "Current password is incorrect", http.StatusBadRequest)
			return
		}
	}

	if err := helpers.Store(r).SetUserPassword(user.ID, pwd.Pwd); err != nil {
		c.log.WithError(err).WithField("user_id", user.ID).Error("Failed to set user password")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	passwordKind := audit.IAMUserPasswordAdminReset
	if editor.ID == user.ID {
		passwordKind = audit.IAMUserPasswordChange
	}
	helpers.Audit(r).Record(r.Context(), audit.Event{Kind: passwordKind, Target: audit.UserTarget(user.ID, user.Username)})

	w.WriteHeader(http.StatusNoContent)
}

func (c *UsersController) DeleteUser(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "_user").(db.User)
	editor := helpers.GetFromContext(r, "user").(*db.User)
	canManageUsers, permissionErr := hasGlobalPermission(r, editor, db.CanManageGlobalUsers)
	if permissionErr != nil {
		helpers.WriteError(w, permissionErr)
		return
	}

	if !canManageUsers && editor.ID != user.ID {
		c.log.WithFields(log.Fields{
			"editor":  editor.Username,
			"user_id": user.ID,
		}).Debug("Not permitted to delete another user")
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if user.Admin && !editor.Admin {
		c.log.WithFields(log.Fields{
			"editor":  editor.Username,
			"user_id": user.ID,
		}).Debug("Delegated user manager cannot delete built-in administrator")
		w.WriteHeader(http.StatusForbidden)
		return
	}

	if err := helpers.Store(r).DeleteUser(user.ID); err != nil {
		c.log.WithError(err).WithField("user_id", user.ID).Error("Failed to delete user")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{Kind: audit.IAMUserDelete, Target: audit.UserTarget(user.ID, user.Username)})

	if err := helpers.Store(r).DeleteOptions(fmt.Sprintf("user%d", user.ID)); err != nil {
		c.log.WithError(err).WithField("user_id", user.ID).Error("Failed to delete options of removed user")
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *UsersController) GetUserIdentities(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "_user").(db.User)

	identities, err := helpers.Store(r).GetUserExternalIdentities(user.ID)
	if err != nil {
		c.log.WithError(err).WithField("user_id", user.ID).Error("Failed to get user identities")
		helpers.WriteErrorStatus(w, "Failed to get identities", http.StatusInternalServerError)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, identities)
}

func (c *UsersController) DeleteUserIdentity(w http.ResponseWriter, r *http.Request) {
	user := helpers.GetFromContext(r, "_user").(db.User)
	idType := mux.Vars(r)["type"]
	provider := mux.Vars(r)["provider"]

	if idType != db.IdentityTypeLdap && idType != db.IdentityTypeOidc {
		helpers.WriteErrorStatus(w, "Invalid identity type", http.StatusBadRequest)
		return
	}

	identities, err := helpers.Store(r).GetUserExternalIdentities(user.ID)
	if err != nil {
		c.log.WithError(err).WithField("user_id", user.ID).Error("Failed to get user identities")
		helpers.WriteErrorStatus(w, "Failed to delete identity", http.StatusInternalServerError)
		return
	}
	// Only block unlinking when the requested identity exists and it is the last one.
	targetExists := false
	for _, identity := range identities {
		if identity.Type == idType && identity.Provider == provider {
			targetExists = true
			break
		}
	}
	if user.External && targetExists && len(identities) <= 1 {
		helpers.WriteErrorStatus(w, errCannotUnlinkLastIdentity.Error(), http.StatusConflict)
		return
	}

	err = helpers.Store(r).DeleteExternalIdentity(user.ID, idType, provider)
	if errors.Is(err, db.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		c.log.WithError(err).WithField("user_id", user.ID).Error("Failed to delete user identity")
		helpers.WriteErrorStatus(w, "Failed to delete identity", http.StatusInternalServerError)
		return
	}

	helpers.Audit(r).Record(r.Context(), audit.Event{
		Kind:     audit.IAMExternalIdentityUnlink,
		Target:   audit.UserTarget(user.ID, user.Username),
		Metadata: audit.AuthMethodMetadata{Method: idType, Provider: provider},
	})

	w.WriteHeader(http.StatusNoContent)
}

func userUpdateMetadata(before db.User, after db.UserWithPwd) audit.UserUpdateMetadata {
	meta := audit.UserUpdateMetadata{Fields: []string{}}
	if before.Username != after.Username {
		meta.Fields = append(meta.Fields, "username")
	}
	if before.Name != after.Name {
		meta.Fields = append(meta.Fields, "name")
	}
	if before.Email != after.Email {
		meta.Fields = append(meta.Fields, "email")
	}
	if before.Alert != after.Alert {
		meta.Fields = append(meta.Fields, "alert")
	}
	if before.Admin != after.Admin {
		meta.Fields = append(meta.Fields, "admin")
		meta.Admin = &audit.BoolChange{Old: before.Admin, New: after.Admin}
	}
	if before.Pro != after.Pro {
		meta.Fields = append(meta.Fields, "pro")
		meta.Pro = &audit.BoolChange{Old: before.Pro, New: after.Pro}
	}
	if after.Pwd != "" {
		meta.Fields = append(meta.Fields, "password")
	}
	return meta
}
