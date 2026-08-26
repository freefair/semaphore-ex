package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	communityserver "github.com/semaphoreui/semaphore/community-pro/services/server"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runnerAuditRecorder struct {
	events []pro_interfaces.AuditEvent
}

type deniedRunnerProvider struct{}

func (deniedRunnerProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityProjectRunners,
			pro_interfaces.CapabilityStateDisabled,
			pro_interfaces.CapabilityReasonDisabledByAdmin,
			nil,
			nil,
		),
	}), nil
}

func (deniedRunnerProvider) Configure(
	context.Context,
	pro_interfaces.CapabilityRequest,
	pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	panic("not used")
}

func (r *runnerAuditRecorder) Record(_ context.Context, event pro_interfaces.AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func TestProjectRunnerCreateReturnsRegistrationTokenOnceAndListsOnlyOriginProject(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "origin"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(
		communityserver.NewSubscriptionService(nil, nil, nil, nil),
		server.NewRunnerService(store),
		features.NewCapabilityProvider(store),
		audit,
	)

	create := httptest.NewRequest(http.MethodPost, "/api/project/1/runners", bytes.NewBufferString(`{"name":"isolated"}`))
	create = runnerContractRequest(create, store, project)
	created := httptest.NewRecorder()
	controller.AddRunner(created, create)

	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		db.Runner
		RegistrationToken string `json:"registration_token"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	assert.NotEmpty(t, response.RegistrationToken)
	assert.Equal(t, project.ID, *response.ProjectID)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionProjectRunnerCreate, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeAllowed, audit.events[0].Outcome)

	originList := httptest.NewRecorder()
	originRequest := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil), store, project)
	controller.GetRunners(originList, originRequest)
	require.Equal(t, http.StatusOK, originList.Code)
	assert.Contains(t, originList.Body.String(), "isolated")
	assert.NotContains(t, originList.Body.String(), response.RegistrationToken)

	otherList := httptest.NewRecorder()
	otherRequest := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/2/runners", nil), store, other)
	controller.GetRunners(otherList, otherRequest)
	require.Equal(t, http.StatusOK, otherList.Code)
	assert.JSONEq(t, `[]`, otherList.Body.String())
}

func TestProjectRunnerMiddlewareRejectsCrossProjectLookup(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	origin, err := store.CreateProject(db.Project{Name: "origin"})
	require.NoError(t, err)
	other, err := store.CreateProject(db.Project{Name: "other"})
	require.NoError(t, err)
	runner, _, err := server.NewRunnerService(store).CreateProjectRunner(db.Runner{
		Name: "isolated", ProjectID: &origin.ID,
	})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), features.NewCapabilityProvider(store), audit)
	handler := controller.RunnerMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("cross-project runner must not reach the handler")
	}))
	request := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/2/runners/1", nil), store, other)
	request = mux.SetURLVars(request, map[string]string{"project_id": "2", "runner_id": "1"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.Equal(t, "runner:"+strconv.Itoa(runner.ID), audit.events[0].TargetID)
}

func TestProjectRunnerCapabilityIsRequiredByBackend(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "guarded"})
	require.NoError(t, err)
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), nil, nil)
	request := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil), store, project)
	response := httptest.NewRecorder()

	controller.GetRunners(response, request)

	assert.Equal(t, http.StatusNotFound, response.Code)
}

func TestProjectRunnerCapabilityDenialIsAudited(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	project, err := store.CreateProject(db.Project{Name: "denied"})
	require.NoError(t, err)
	audit := &runnerAuditRecorder{}
	controller := NewProjectRunnerController(nil, server.NewRunnerService(store), deniedRunnerProvider{}, audit)
	request := runnerContractRequest(httptest.NewRequest(http.MethodGet, "/api/project/1/runners", nil), store, project)
	response := httptest.NewRecorder()

	controller.GetRunners(response, request)

	assert.Equal(t, http.StatusForbidden, response.Code)
	require.Len(t, audit.events, 1)
	assert.Equal(t, pro_interfaces.AuditActionProjectRunnerList, audit.events[0].Action)
	assert.Equal(t, pro_interfaces.AuditOutcomeDenied, audit.events[0].Outcome)
	assert.Equal(t, string(pro_interfaces.CapabilityReasonDisabledByAdmin), audit.events[0].Reason)
}

func runnerContractRequest(request *http.Request, store db.Store, project db.Project) *http.Request {
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "project", project)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 1})
	return request
}
