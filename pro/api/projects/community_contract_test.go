package projects

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommunityProjectCollectionsAreEmpty(t *testing.T) {
	runnerController := NewProjectRunnerController(nil, nil)
	terraformController := NewTerraformInventoryController(nil)
	workflowController := NewWorkflowController(nil, nil)

	handlers := map[string]http.HandlerFunc{
		"runners":            runnerController.GetRunners,
		"runner tags":        runnerController.GetRunnerTags,
		"terraform aliases":  terraformController.GetTerraformInventoryAliases,
		"terraform states":   terraformController.GetTerraformInventoryStates,
		"workflows":          workflowController.GetWorkflows,
		"workflow runs":      workflowController.GetWorkflowRuns,
		"workflow approvals": workflowController.GetWorkflowApprovals,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, http.StatusOK, response.Code)
			assert.JSONEq(t, "[]", response.Body.String())
		})
	}
}

func TestCommunityProjectResourcesAreNotFound(t *testing.T) {
	runnerController := NewProjectRunnerController(nil, nil)
	terraformController := NewTerraformInventoryController(nil)
	workflowController := NewWorkflowController(nil, nil)

	handlers := map[string]http.HandlerFunc{
		"add runner":                runnerController.AddRunner,
		"get runner":                runnerController.GetRunner,
		"update runner":             runnerController.UpdateRunner,
		"delete runner":             runnerController.DeleteRunner,
		"activate runner":           runnerController.SetRunnerActive,
		"clear runner cache":        runnerController.ClearRunnerCache,
		"add terraform alias":       terraformController.AddTerraformInventoryAlias,
		"get terraform alias":       terraformController.GetTerraformInventoryAlias,
		"delete terraform alias":    terraformController.DeleteTerraformInventoryAlias,
		"set terraform key":         terraformController.SetTerraformInventoryAliasAccessKey,
		"latest terraform state":    terraformController.GetTerraformInventoryLatestState,
		"get terraform state":       terraformController.GetTerraformInventoryState,
		"delete terraform state":    terraformController.DeleteTerraformInventoryState,
		"add workflow":              workflowController.AddWorkflow,
		"get workflow":              workflowController.GetWorkflow,
		"update workflow":           workflowController.UpdateWorkflow,
		"remove workflow":           workflowController.RemoveWorkflow,
		"run workflow":              workflowController.RunWorkflow,
		"stop workflow":             workflowController.StopWorkflowRun,
		"get workflow run":          workflowController.GetWorkflowRun,
		"get workflow artifacts":    workflowController.GetWorkflowRunArtifacts,
		"resolve workflow approval": workflowController.ResolveWorkflowApproval,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
			assert.Equal(t, http.StatusNotFound, response.Code)
		})
	}
}

func TestCommunityRunnerMiddlewareHasNoSideEffects(t *testing.T) {
	controller := NewProjectRunnerController(nil, nil)
	called := false
	handler := controller.RunnerMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))

	require.True(t, called)
	assert.Equal(t, http.StatusNoContent, response.Code)
}

func TestCommunityRunnerTokenRegenerationReturnsNoMaterial(t *testing.T) {
	controller := NewProjectRunnerController(nil, nil)
	response := httptest.NewRecorder()

	controller.RegenerateRegistrationToken(response, httptest.NewRequest(http.MethodPost, "/", nil))

	assert.Equal(t, http.StatusCreated, response.Code)
	assert.JSONEq(t, "{}", response.Body.String())
}
