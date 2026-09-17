package projects

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
)

type terraformAliasStoreStub struct {
	created db.TerraformInventoryAlias
	updated db.TerraformInventoryAlias
}

func (s *terraformAliasStoreStub) CreateTerraformInventoryAlias(alias db.TerraformInventoryAlias) (db.TerraformInventoryAlias, error) {
	s.created = alias
	return alias, nil
}
func (s *terraformAliasStoreStub) GetTerraformInventoryAliasByAlias(string) (db.TerraformInventoryAlias, error) {
	return db.TerraformInventoryAlias{}, db.ErrNotFound
}
func (s *terraformAliasStoreStub) GetTerraformInventoryAlias(projectID, inventoryID int, alias string) (db.TerraformInventoryAlias, error) {
	if s.created.ProjectID == projectID && s.created.InventoryID == inventoryID && s.created.Alias == alias {
		return s.created, nil
	}
	return db.TerraformInventoryAlias{}, db.ErrNotFound
}
func (s *terraformAliasStoreStub) GetTerraformInventoryAliases(int, int) ([]db.TerraformInventoryAlias, error) {
	return nil, nil
}
func (s *terraformAliasStoreStub) UpdateTerraformInventoryAlias(alias db.TerraformInventoryAlias) error {
	s.updated = alias
	return nil
}
func (s *terraformAliasStoreStub) DeleteTerraformInventoryAlias(int, int, string) error { return nil }
func (s *terraformAliasStoreStub) CreateTerraformInventoryState(db.TerraformInventoryState) (db.TerraformInventoryState, error) {
	return db.TerraformInventoryState{}, nil
}
func (s *terraformAliasStoreStub) GetTerraformInventoryState(int, int, int) (db.TerraformInventoryState, error) {
	return db.TerraformInventoryState{}, db.ErrNotFound
}
func (s *terraformAliasStoreStub) GetTerraformInventoryStates(int, int, db.RetrieveQueryParams) ([]db.TerraformInventoryState, error) {
	return nil, nil
}
func (s *terraformAliasStoreStub) DeleteTerraformInventoryState(int, int, int) error { return nil }
func (s *terraformAliasStoreStub) GetTerraformStateCount() (int, error)              { return 0, nil }

func terraformAliasRequest(body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	request = mux.SetURLVars(request, map[string]string{"alias_id": "not-exposed"})
	request = helpers.SetContextValue(request, "project", db.Project{ID: 7})
	return helpers.SetContextValue(request, "inventory", db.Inventory{ID: 11, ProjectID: 7, Type: db.InventoryTerraformWorkspace})
}

func TestTerraformInventoryAliasCreateUsesRouteScopeAndGeneratedIdentifier(t *testing.T) {
	util.Config = &util.ConfigType{Port: ":3000"}
	store := &terraformAliasStoreStub{}
	response := httptest.NewRecorder()

	NewTerraformInventoryController(store).AddTerraformInventoryAlias(response, terraformAliasRequest(`{"project_id":7,"auth_key_id":17}`))

	assert.Equal(t, http.StatusCreated, response.Code)
	assert.Equal(t, 7, store.created.ProjectID)
	assert.Equal(t, 11, store.created.InventoryID)
	assert.Equal(t, 17, store.created.AuthKeyID)
	assert.NotEmpty(t, store.created.Alias)
	assert.Contains(t, response.Body.String(), `"url":"http://localhost:3000/api/terraform/`)
	assert.NotContains(t, response.Body.String(), "not-exposed")
}

func TestTerraformInventoryAliasUpdateAcceptsUIResponseAndRejectsScopeMismatch(t *testing.T) {
	util.Config = &util.ConfigType{Port: ":3000"}
	store := &terraformAliasStoreStub{created: db.TerraformInventoryAlias{Alias: "ui-alias", ProjectID: 7, InventoryID: 11, AuthKeyID: 17}}
	controller := NewTerraformInventoryController(store)
	request := terraformAliasRequest(`{"id":"ui-alias","project_id":7,"inventory_id":11,"auth_key_id":18,"url":"http://localhost:3000/api/terraform/ui-alias"}`)
	request = mux.SetURLVars(request, map[string]string{"alias_id": "ui-alias"})
	response := httptest.NewRecorder()
	controller.SetTerraformInventoryAliasAccessKey(response, request)
	assert.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 18, store.updated.AuthKeyID)

	response = httptest.NewRecorder()
	controller.SetTerraformInventoryAliasAccessKey(response, terraformAliasRequest(`{"project_id":99,"auth_key_id":18}`))
	assert.Equal(t, http.StatusBadRequest, response.Code)
}

func TestTerraformInventoryAliasCreateRejectsCallerControlledIdentity(t *testing.T) {
	store := &terraformAliasStoreStub{}
	response := httptest.NewRecorder()

	NewTerraformInventoryController(store).AddTerraformInventoryAlias(response, terraformAliasRequest(`{"auth_key_id":17,"alias":"chosen-by-caller"}`))

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Empty(t, store.created.Alias)
}

func TestTerraformStateManagementReportsUnavailableBackend(t *testing.T) {
	controller := NewTerraformInventoryController(nil)
	tests := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"list", controller.GetTerraformInventoryStates},
		{"latest", controller.GetTerraformInventoryLatestState},
		{"state", controller.GetTerraformInventoryState},
		{"delete", controller.DeleteTerraformInventoryState},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := terraformAliasRequest("")
			request = mux.SetURLVars(request, map[string]string{"state_id": "1"})
			response := httptest.NewRecorder()
			tt.handler(response, request)
			assert.Equal(t, http.StatusServiceUnavailable, response.Code)
		})
	}
}
