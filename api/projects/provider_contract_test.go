package projects

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type providerContractStore struct {
	db.Store
	inventory db.Inventory
}

func (s *providerContractStore) CreateInventory(v db.Inventory) (db.Inventory, error) {
	v.ID = 3
	s.inventory = v
	return v, nil
}
func (s *providerContractStore) UpdateInventory(v db.Inventory) error     { s.inventory = v; return nil }
func (s *providerContractStore) CreateEvent(v db.Event) (db.Event, error) { return v, nil }
func (s *providerContractStore) GetIntegrationMatcher(int, int, int) (db.IntegrationMatcher, error) {
	return db.IntegrationMatcher{}, db.ErrNotFound
}
func (s *providerContractStore) GetIntegrationExtractValue(int, int, int) (db.IntegrationExtractValue, error) {
	return db.IntegrationExtractValue{}, db.ErrNotFound
}

type providerContractLog struct{ pro_interfaces.LogWriteService }

func (providerContractLog) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }

func TestProviderContractTerragruntInventory(t *testing.T) {
	store := &providerContractStore{}
	for _, update := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "update"}[update], func(t *testing.T) {
			v := db.Inventory{ProjectID: 1, Name: "workspace", Type: db.InventoryTerragruntWorkspace, Inventory: "production"}
			if update {
				v.ID = 3
			}
			payload, err := json.Marshal(v)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(payload))
			req = helpers.SetContextValue(req, "project", db.Project{ID: 1})
			req = helpers.SetContextValue(req, "inventory", v)
			req = helpers.SetContextValue(req, "store", store)
			req = helpers.SetContextValue(req, "user", &db.User{ID: 1})
			req = helpers.SetContextValue(req, "log_writer", providerContractLog{})
			response := httptest.NewRecorder()
			if update {
				UpdateInventory(response, req)
				assert.Equal(t, http.StatusNoContent, response.Code)
			} else {
				AddInventory(response, req)
				assert.Equal(t, http.StatusCreated, response.Code)
			}
			assert.Equal(t, db.InventoryTerragruntWorkspace, store.inventory.Type)
		})
	}
}

func TestProviderContractIntegrationNotFound(t *testing.T) {
	cases := []struct {
		name, param string
		handler     http.HandlerFunc
	}{{"matcher", "matcher_id", GetIntegrationMatcher}, {"value", "value_id", GetIntegrationExtractValue}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/", nil), map[string]string{tc.param: "3"})
			req = helpers.SetContextValue(req, "project", db.Project{ID: 1})
			req = helpers.SetContextValue(req, "integration", db.Integration{ID: 2, ProjectID: 1})
			req = helpers.SetContextValue(req, "store", &providerContractStore{})
			response := httptest.NewRecorder()
			tc.handler(response, req)
			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}
