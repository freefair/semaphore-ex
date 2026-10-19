package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	coresql "github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pkg/metrics"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoleMappingRoutesRequireAdminAndUseEnhancedStore(t *testing.T) {
	previousConfig := util.Config
	t.Cleanup(func() { util.Config = previousConfig })
	store := coresql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	util.Config = &util.ConfigType{Debugging: &util.DebuggingConfig{}, OidcProviders: map[string]util.OidcProvider{
		"corp": {GroupsClaim: "groups"},
	}}
	project, err := store.CreateProject(db.Project{Name: "mapped-project"})
	require.NoError(t, err)
	admin, err := store.CreateUserWithoutPassword(db.User{Username: "mapping-admin", Name: "Mapping admin", Email: "mapping-admin@example.test", Admin: true})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "mapping-admin-token", UserID: admin.ID, Name: "role mappings admin"})
	require.NoError(t, err)
	nonAdmin, err := store.CreateUserWithoutPassword(db.User{Username: "mapping-user", Name: "Mapping user", Email: "mapping-user@example.test"})
	require.NoError(t, err)
	_, err = store.CreateAPIToken(db.APIToken{ID: "mapping-user-token", UserID: nonAdmin.ID, Name: "role mappings user"})
	require.NoError(t, err)

	router := Route(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, metrics.NewMetrics(), nil, nil)
	withStore := func(request *http.Request) *http.Request {
		return helpers.SetContextValue(request, "store", store)
	}

	denied := withStore(httptest.NewRequest(http.MethodGet, "/api/role_mappings", nil))
	denied.Header.Set("Authorization", "Bearer mapping-user-token")
	deniedResponse := httptest.NewRecorder()
	router.ServeHTTP(deniedResponse, denied)
	assert.Equal(t, http.StatusForbidden, deniedResponse.Code)

	create := withStore(httptest.NewRequest(http.MethodPost, "/api/role_mappings", bytes.NewBufferString(`{"provider":"corp","group":"operators","project_id":`+strconv.Itoa(project.ID)+`,"role":"manager"}`)))
	create.Header.Set("Authorization", "Bearer mapping-admin-token")
	create.Header.Set("Content-Type", "application/json")
	createdResponse := httptest.NewRecorder()
	router.ServeHTTP(createdResponse, create)
	require.Equal(t, http.StatusCreated, createdResponse.Code, createdResponse.Body.String())
	var created db.RoleMapping
	require.NoError(t, json.Unmarshal(createdResponse.Body.Bytes(), &created))
	require.NotZero(t, created.ID)

	list := withStore(httptest.NewRequest(http.MethodGet, "/api/role_mappings", nil))
	list.Header.Set("Authorization", "Bearer mapping-admin-token")
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, list)
	require.Equal(t, http.StatusOK, listResponse.Code, listResponse.Body.String())
	assert.Contains(t, listResponse.Body.String(), `"provider":"corp"`)

	deleteRequest := withStore(httptest.NewRequest(http.MethodDelete, "/api/role_mappings/"+strconv.Itoa(created.ID), nil))
	deleteRequest.Header.Set("Authorization", "Bearer mapping-admin-token")
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	assert.Equal(t, http.StatusOK, deleteResponse.Code, deleteResponse.Body.String())
}
