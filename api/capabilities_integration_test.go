package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	sqldb "github.com/semaphoreui/semaphore/db/sql"
	proFeatures "github.com/semaphoreui/semaphore/pro/pkg/features"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	capabilityServices "github.com/semaphoreui/semaphore/services/capabilities"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapabilityHTTPIntegrationMatchesSelectedEdition(t *testing.T) {
	store := sqldb.InitConfigCreateTestStore()
	defer store.Close()
	facade := capabilityServices.NewServiceFacade(
		proFeatures.NewCapabilityProvider(store),
		proFeatures.NewCapabilityTestService(store),
	)
	controller := NewCapabilityController(facade)
	admin := &db.User{ID: 7, Admin: true}

	if proFeatures.Compatibility().Edition == pro_interfaces.EditionCommunity {
		recorder := serveCapabilityRequest(
			controller,
			pro_interfaces.CapabilityAccessWrite,
			controller.CreateRecord,
			http.MethodPost,
			`{"value":"blocked"}`,
			admin,
		)
		assert.Equal(t, http.StatusNotFound, recorder.Code)
		assert.Contains(t, recorder.Body.String(), `"reason":"provider_unavailable"`)
		return
	}

	configureCapability(t, controller, admin, pro_interfaces.CapabilityStateActive)
	created := serveCapabilityRequest(
		controller,
		pro_interfaces.CapabilityAccessWrite,
		controller.CreateRecord,
		http.MethodPost,
		`{"value":"preserved"}`,
		admin,
	)
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	configureCapability(t, controller, admin, pro_interfaces.CapabilityStateDisabled)
	denied := serveCapabilityRequest(
		controller,
		pro_interfaces.CapabilityAccessExecute,
		controller.RunBackgroundAction,
		http.MethodPost,
		`{"value":"blocked"}`,
		admin,
	)
	assert.Equal(t, http.StatusForbidden, denied.Code)
	assert.Contains(t, denied.Body.String(), `"reason":"disabled_by_admin"`)

	configureCapability(t, controller, admin, pro_interfaces.CapabilityStateActive)
	listed := serveCapabilityRequest(
		controller,
		pro_interfaces.CapabilityAccessRead,
		controller.ListRecords,
		http.MethodGet,
		"",
		admin,
	)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	assert.Contains(t, listed.Body.String(), `"value":"preserved"`)
}

func configureCapability(
	t *testing.T,
	controller *CapabilityController,
	admin *db.User,
	state pro_interfaces.CapabilityState,
) {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/capabilities/lifecycle-test",
		bytes.NewBufferString(`{"state":"`+string(state)+`"}`),
	)
	request = helpers.SetContextValue(request, "user", admin)
	recorder := httptest.NewRecorder()
	controller.Configure(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
}

func serveCapabilityRequest(
	controller *CapabilityController,
	access pro_interfaces.CapabilityAccess,
	handler http.HandlerFunc,
	method string,
	body string,
	user *db.User,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/", bytes.NewBufferString(body))
	request = helpers.SetContextValue(request, "user", user)
	recorder := httptest.NewRecorder()
	guarded := controller.SnapshotMiddleware(controller.Require(access)(handler))
	guarded.ServeHTTP(recorder, request)
	return recorder
}
