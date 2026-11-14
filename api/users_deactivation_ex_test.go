package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDelegatedUserManagerCannotDeactivateAdministrator(t *testing.T) {
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	manager, err := store.CreateUserWithoutPassword(db.User{
		Username: "delegated-lifecycle-manager", Name: "Delegated", Email: "delegated-lifecycle@example.test",
	})
	require.NoError(t, err)
	role, err := store.CreateGlobalRole(db.Role{
		ID: "delegated_lifecycle", Slug: "delegated_lifecycle", Name: "Delegated lifecycle",
		GlobalPermissions: db.CanManageGlobalUsers, Revision: 1,
	})
	require.NoError(t, err)
	_, err = store.CreateGlobalRoleAssignment(db.GlobalRoleAssignment{UserID: manager.ID, RoleID: role.ID, Revision: 1})
	require.NoError(t, err)
	admin, err := store.CreateUserWithoutPassword(db.User{
		Username: "protected-admin", Name: "Protected admin", Email: "protected-admin@example.test", Admin: true,
	})
	require.NoError(t, err)
	member, err := store.CreateUserWithoutPassword(db.User{
		Username: "plain-member", Name: "Plain member", Email: "plain-member@example.test",
	})
	require.NoError(t, err)

	deactivate := func(target db.User) int {
		request := httptest.NewRequest(http.MethodPut, "/api/users/0/deactivation", bytes.NewBufferString(`{"disabled":true}`))
		request.Header.Set("Content-Type", "application/json")
		request = helpers.SetContextValue(request, "store", store)
		request = helpers.SetContextValue(request, "user", &manager)
		request = helpers.SetContextValue(request, "_user", target)
		request = withActiveProjectRolesSnapshot(request, manager)
		recorder := httptest.NewRecorder()
		NewUsersController().SetUserDisabled(recorder, request)
		return recorder.Code
	}

	assert.Equal(t, http.StatusForbidden, deactivate(admin))
	stored, err := store.GetUser(admin.ID)
	require.NoError(t, err)
	assert.False(t, stored.Disabled)

	assert.Equal(t, http.StatusNoContent, deactivate(member))
	stored, err = store.GetUser(member.ID)
	require.NoError(t, err)
	assert.True(t, stored.Disabled)
}
