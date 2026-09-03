package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityEmailVerificationIsForbidden(t *testing.T) {
	response := httptest.NewRecorder()

	VerifySessionByEmail(&db.Session{}, response, httptest.NewRequest(http.MethodPost, "/", nil))

	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, response.Body.String())
}

func TestCommunityRoleResourcesAreNotFound(t *testing.T) {
	controller := NewRolesController(nil, nil)
	for name, handler := range map[string]http.HandlerFunc{
		"get global":     controller.GetGlobalRole,
		"add global":     controller.AddRole,
		"update global":  controller.UpdateRole,
		"delete global":  controller.DeleteRole,
		"add project":    controller.AddProjectRole,
		"get project":    controller.GetProjectRole,
		"update project": controller.UpdateProjectRole,
		"delete project": controller.DeleteProjectRole,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}

func TestCommunityRoleCollectionsAreEmpty(t *testing.T) {
	controller := NewRolesController(nil, nil)
	for name, handler := range map[string]http.HandlerFunc{
		"global":  controller.GetRoles,
		"project": controller.GetProjectRoles,
		"all":     controller.GetProjectAndGlobalRoles,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, http.StatusOK, response.Code)
			assert.JSONEq(t, "[]", response.Body.String())
		})
	}
}

func TestCommunityTerraformMiddlewareHasNoSideEffects(t *testing.T) {
	controller := NewTerraformController(nil, nil, nil)
	called := false
	handler := controller.TerraformInventoryAliasMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	require.True(t, called)
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestCommunityTerraformRoutesAreNotFound(t *testing.T) {
	controller := NewTerraformController(nil, nil, nil)
	for name, handler := range map[string]http.HandlerFunc{
		"get":    controller.GetTerraformState,
		"add":    controller.AddTerraformState,
		"lock":   controller.LockTerraformState,
		"unlock": controller.UnlockTerraformState,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}
