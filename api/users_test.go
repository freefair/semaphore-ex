package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
)

func newPasswordRequest(store db.Store, editor *db.User, target db.User, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/users/1/password", bytes.NewBufferString(body))
	r = helpers.SetContextValue(r, "store", store)
	r = helpers.SetContextValue(r, "user", editor)
	r = helpers.SetContextValue(r, "_user", target)
	return r
}

func TestUpdateUserPassword_SelfRequiresCurrentPassword(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	user := createUserOptionsTestUser(t, store, "self") // password: verystrongpassword1

	t.Run("correct current password", func(t *testing.T) {
		r := newPasswordRequest(store, &user, user,
			`{"current_password":"verystrongpassword1","password":"newpassword2"}`)
		w := httptest.NewRecorder()
		NewUsersController().UpdateUserPassword(w, r)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("wrong current password is rejected", func(t *testing.T) {
		r := newPasswordRequest(store, &user, user,
			`{"current_password":"wrong","password":"newpassword3"}`)
		w := httptest.NewRecorder()
		NewUsersController().UpdateUserPassword(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing current password is rejected", func(t *testing.T) {
		r := newPasswordRequest(store, &user, user, `{"password":"newpassword4"}`)
		w := httptest.NewRecorder()
		NewUsersController().UpdateUserPassword(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestUpdateUserPassword_AdminExemptForOtherUsers(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	admin := createUserOptionsTestUser(t, store, "admin")
	admin.Admin = true
	target := createUserOptionsTestUser(t, store, "target")

	// Admin changing someone else's password does not need the current one.
	r := newPasswordRequest(store, &admin, target, `{"password":"resetbyanadmin1"}`)
	w := httptest.NewRecorder()
	NewUsersController().UpdateUserPassword(w, r)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestUsersControllerNormalizesCommercialProState(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "admin", Name: "Admin", Email: "admin@example.test", Admin: true,
	})
	assert.NoError(t, err)
	controller := NewUsersController()

	create := httptest.NewRequest(http.MethodPost, "/api/users", bytes.NewBufferString(
		`{"username":"created","name":"Created","email":"created@example.test","password":"strongpassword1","pro":true}`,
	))
	create.Header.Set("Content-Type", "application/json")
	create = helpers.SetContextValue(create, "store", store)
	create = helpers.SetContextValue(create, "user", &admin)
	created := httptest.NewRecorder()
	controller.AddUser(created, create)
	assert.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	var createdUser db.User
	assert.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdUser))
	user, err := store.GetUser(createdUser.ID)
	assert.NoError(t, err)
	assert.False(t, user.Pro)

	update := httptest.NewRequest(http.MethodPut, "/api/users/2", bytes.NewBufferString(
		`{"id":2,"username":"created","name":"Created","email":"created@example.test","pro":true}`,
	))
	update.Header.Set("Content-Type", "application/json")
	update = helpers.SetContextValue(update, "store", store)
	update = helpers.SetContextValue(update, "user", &admin)
	update = helpers.SetContextValue(update, "_user", user)
	updated := httptest.NewRecorder()
	controller.UpdateUser(updated, update)
	assert.Equal(t, http.StatusNoContent, updated.Code, updated.Body.String())

	user, err = store.GetUser(user.ID)
	assert.NoError(t, err)
	assert.False(t, user.Pro)
}
