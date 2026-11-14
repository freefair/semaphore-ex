package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The directory group search reveals group names and DNs, so only
// administrators may call it.
func TestLDAPGroupSearchRouteRequiresAdministrator(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config = &util.ConfigType{Debugging: &util.DebuggingConfig{}}
	member, err := store.CreateUserWithoutPassword(db.User{
		Username: "search-member", Name: "Search member", Email: "search-member@example.test",
	})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "group-search-member-key", UserID: member.ID, Name: "group search member"})
	require.NoError(t, err)

	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, metrics.NewMetrics(), nil, nil)
	search := func(key string) int {
		request := httptest.NewRequest(http.MethodGet, "/api/capabilities/ldap/groups?provider_id=corp&query=ops", nil)
		request = helpers.SetContextValue(request, "store", store)
		request.Header.Set("Authorization", "Bearer "+key)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder.Code
	}

	// The route sits behind the administrator middleware: a member is refused
	// before the controller or the directory is reached.
	assert.Equal(t, http.StatusForbidden, search("group-search-member-key"))
}
