package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	proServer "github.com/semaphoreui/semaphore/pro/services/server"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The deactivation endpoint was registered on a router that matches
// /users/{user_id} exactly, so every request answered 404. This goes through
// the real router.
func TestUserDeactivationRouteIsReachableAndGuarded(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config = &util.ConfigType{Debugging: &util.DebuggingConfig{}}

	admin, err := store.CreateUserWithoutPassword(db.User{Username: "lifecycle-admin", Name: "Lifecycle admin", Email: "lifecycle-admin@example.test", Admin: true})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "lifecycle-admin-key", UserID: admin.ID, Name: "lifecycle admin"})
	require.NoError(t, err)
	member, err := store.CreateUserWithoutPassword(db.User{Username: "lifecycle-member", Name: "Lifecycle member", Email: "lifecycle-member@example.test"})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "lifecycle-member-key", UserID: member.ID, Name: "lifecycle member"})
	require.NoError(t, err)
	leaver, err := store.CreateUserWithoutPassword(db.User{Username: "lifecycle-leaver", Name: "Lifecycle leaver", Email: "lifecycle-leaver@example.test"})
	require.NoError(t, err)

	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, proServer.NewLogWriteService(), nil, metrics.NewMetrics(), nil, nil)
	deactivate := func(key string, userID int, disabled bool) int {
		body := `{"disabled":` + strconv.FormatBool(disabled) + `}`
		request := httptest.NewRequest(http.MethodPut, "/api/users/"+strconv.Itoa(userID)+"/deactivation", bytes.NewBufferString(body))
		request = helpers.SetContextValue(request, "store", store)
		request.Header.Set("Authorization", "Bearer "+key)
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code
	}

	// GetUserMiddleware refuses access to another user with 401 (upstream).
	assert.Equal(t, http.StatusUnauthorized, deactivate("lifecycle-member-key", leaver.ID, true), "a plain member may not deactivate anyone")
	untouched, err := store.GetUser(leaver.ID)
	require.NoError(t, err)
	assert.False(t, untouched.Disabled)

	require.Equal(t, http.StatusNoContent, deactivate("lifecycle-admin-key", leaver.ID, true))
	stored, err := store.GetUser(leaver.ID)
	require.NoError(t, err)
	assert.True(t, stored.Disabled)

	require.Equal(t, http.StatusNoContent, deactivate("lifecycle-admin-key", leaver.ID, false))
	stored, err = store.GetUser(leaver.ID)
	require.NoError(t, err)
	assert.False(t, stored.Disabled)
}
