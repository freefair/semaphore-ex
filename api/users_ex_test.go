package api

import (
	"bytes"
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
