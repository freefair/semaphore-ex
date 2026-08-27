package projects

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type runtimeSecretStorageServiceStub struct {
	created   db.SecretStorage
	updated   db.SecretStorage
	deleted   int
	health    pro_interfaces.SecretProviderHealth
	operation db.SecretSyncOperation
	history   []db.SecretSyncOperation
	requestID string
	resolveID *int
}

type runtimeSecretAPIStore struct {
	db.Store
	sync db.SecretSync
}

func (runtimeSecretAPIStore) CreateEvent(event db.Event) (db.Event, error) { return event, nil }
func (s runtimeSecretAPIStore) GetStorageSecretSync(int) (db.SecretSync, error) {
	return s.sync, nil
}

type runtimeSecretLogWriter struct{}

func (runtimeSecretLogWriter) WriteEventLog(pro_interfaces.EventLogRecord) error { return nil }
func (runtimeSecretLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error   { return nil }
func (runtimeSecretLogWriter) WriteResult(any) error                             { return nil }

func (s *runtimeSecretStorageServiceStub) GetSecretStorage(int, int) (db.SecretStorage, error) {
	return db.SecretStorage{}, nil
}
func (s *runtimeSecretStorageServiceStub) Update(storage db.SecretStorage) error {
	s.updated = storage
	return nil
}
func (s *runtimeSecretStorageServiceStub) Delete(_ int, storageID int) error {
	s.deleted = storageID
	return nil
}
func (s *runtimeSecretStorageServiceStub) GetSecretStorages(int) ([]db.SecretStorage, error) {
	return []db.SecretStorage{}, nil
}
func (s *runtimeSecretStorageServiceStub) Create(storage db.SecretStorage) (db.SecretStorage, error) {
	s.created = storage
	storage.ID = 9
	storage.Secret = ""
	return storage, nil
}
func (s *runtimeSecretStorageServiceStub) SyncSecrets(db.SecretSync) error { return nil }
func (s *runtimeSecretStorageServiceStub) RequestSecretSync(
	_ context.Context,
	_ db.SecretSync,
	requestID string,
	_ *int,
	resolveID *int,
) (db.SecretSyncOperation, error) {
	s.requestID = requestID
	s.resolveID = resolveID
	return s.operation, nil
}
func (s *runtimeSecretStorageServiceStub) RunSecretSyncOperation(
	context.Context,
	db.SecretSyncOperation,
) (db.SecretSyncOperation, error) {
	return s.operation, nil
}
func (s *runtimeSecretStorageServiceStub) GetSecretSyncHistory(
	int,
	int,
	int,
) ([]db.SecretSyncOperation, error) {
	return s.history, nil
}
func (s *runtimeSecretStorageServiceStub) TestConnection(
	context.Context,
	int,
	int,
) (pro_interfaces.SecretProviderHealth, error) {
	return s.health, nil
}

type projectRuntimeCapabilityProvider struct {
	state pro_interfaces.CapabilityState
}

func (p projectRuntimeCapabilityProvider) Resolve(
	_ context.Context,
	request pro_interfaces.CapabilityRequest,
) (pro_interfaces.CapabilitySnapshot, error) {
	state := p.state
	if state == "" {
		state = pro_interfaces.CapabilityStateActive
	}
	reason := pro_interfaces.CapabilityReasonActive
	var access []pro_interfaces.CapabilityAccess
	if state == pro_interfaces.CapabilityStateActive {
		access = []pro_interfaces.CapabilityAccess{
			pro_interfaces.CapabilityAccessRead,
			pro_interfaces.CapabilityAccessWrite,
			pro_interfaces.CapabilityAccessExecute,
		}
	} else {
		reason = pro_interfaces.CapabilityReasonDisabledByAdmin
		access = []pro_interfaces.CapabilityAccess{pro_interfaces.CapabilityAccessRead}
	}
	return pro_interfaces.NewCapabilitySnapshot(request, []pro_interfaces.CapabilityDecision{
		pro_interfaces.NewCapabilityDecision(
			pro_interfaces.CapabilityRuntimeSecrets, state, reason, access, nil,
		),
	}), nil
}

func (p projectRuntimeCapabilityProvider) Configure(
	context.Context,
	pro_interfaces.CapabilityRequest,
	pro_interfaces.CapabilityConfiguration,
) (pro_interfaces.CapabilitySnapshot, error) {
	return pro_interfaces.CapabilitySnapshot{}, nil
}

func TestSecretStorageCreateAndUpdateKeepCredentialsWriteOnly(t *testing.T) {
	service := &runtimeSecretStorageServiceStub{}
	controller := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{})
	createBody := []byte(`{"project_id":3,"name":"OpenBao","type":"openbao","params":{"url":"https://bao.example"},"secret":"bootstrap"}`)
	create := runtimeSecretRequest(http.MethodPost, "/api/project/3/secret_storages", createBody)
	createRecorder := httptest.NewRecorder()

	controller.Add(createRecorder, create)

	require.Equal(t, http.StatusCreated, createRecorder.Code)
	assert.Equal(t, "bootstrap", service.created.Secret, "credential reaches the write-only service input")
	assert.NotContains(t, createRecorder.Body.String(), "bootstrap")

	updateBody := []byte(`{"id":9,"project_id":3,"name":"OpenBao","type":"openbao","params":{"url":"https://bao.example"},"secret":"replacement"}`)
	update := runtimeSecretRequest(http.MethodPut, "/api/project/3/secret_storages/9", updateBody)
	update = helpers.SetContextValue(update, "secretStorage", db.SecretStorage{ID: 9, ProjectID: 3})
	updateRecorder := httptest.NewRecorder()

	controller.Update(updateRecorder, update)

	require.Equal(t, http.StatusOK, updateRecorder.Code)
	assert.Equal(t, "replacement", service.updated.Secret)
	assert.NotContains(t, updateRecorder.Body.String(), "replacement")
}

func TestSecretStorageConnectionTestReturnsSanitizedHealth(t *testing.T) {
	checkedAt := time.Date(2026, 8, 27, 18, 0, 0, 0, time.UTC)
	service := &runtimeSecretStorageServiceStub{health: pro_interfaces.SecretProviderHealth{
		StorageID: 9, State: pro_interfaces.SecretProviderHealthHealthy, CheckedAt: checkedAt,
	}}
	controller := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{})
	request := runtimeSecretRequest(http.MethodPost, "/api/project/3/secret_storages/9/test", nil)
	request = helpers.SetContextValue(request, "secretStorage", db.SecretStorage{ID: 9, ProjectID: 3})
	recorder := httptest.NewRecorder()

	controller.TestConnection(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var health pro_interfaces.SecretProviderHealth
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &health))
	assert.Equal(t, pro_interfaces.SecretProviderHealthHealthy, health.State)
	assert.Equal(t, checkedAt, health.CheckedAt)
	assert.NotContains(t, recorder.Body.String(), "health-token")
}

func TestSecretStorageCapabilityAndProjectPermissionAreBackendAuthoritative(t *testing.T) {
	service := &runtimeSecretStorageServiceStub{}
	disabled := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{
		state: pro_interfaces.CapabilityStateDisabled,
	})
	request := runtimeSecretRequest(http.MethodPost, "/api/project/3/secret_storages", []byte(`{}`))
	recorder := httptest.NewRecorder()
	disabled.Add(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Empty(t, service.created.Name)

	active := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{})
	forbiddenRequest := runtimeSecretRequest(http.MethodPost, "/api/project/3/secret_storages", []byte(`{}`))
	forbiddenRequest = helpers.SetContextValue(forbiddenRequest, "user", &db.User{ID: 8, Admin: false})
	forbiddenRequest = helpers.SetContextValue(forbiddenRequest, "permissions", db.ProjectUserPermission(0))
	forbiddenRecorder := httptest.NewRecorder()
	GetMustCanMiddleware(db.CanManageProjectResources)(http.HandlerFunc(active.Add)).ServeHTTP(
		forbiddenRecorder, forbiddenRequest,
	)
	assert.Equal(t, http.StatusForbidden, forbiddenRecorder.Code)
}

func TestSecretStorageDeleteRequiresWriteCapability(t *testing.T) {
	service := &runtimeSecretStorageServiceStub{}
	active := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{})
	request := runtimeSecretRequest(http.MethodDelete, "/api/project/3/secret_storages/9", nil)
	request = mux.SetURLVars(request, map[string]string{"storage_id": "9"})
	recorder := httptest.NewRecorder()

	active.Remove(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Equal(t, 9, service.deleted)

	service.deleted = 0
	disabled := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{
		state: pro_interfaces.CapabilityStateDisabled,
	})
	deniedRecorder := httptest.NewRecorder()
	disabled.Remove(deniedRecorder, request)
	assert.Equal(t, http.StatusForbidden, deniedRecorder.Code)
	assert.Zero(t, service.deleted)
}

func TestSecretStorageManualSyncReturnsConflictAndAcceptsExplicitResolution(t *testing.T) {
	service := &runtimeSecretStorageServiceStub{operation: db.SecretSyncOperation{
		ID: 41, RequestID: "manual:api-0001", Status: db.SecretSyncOperationConflict,
		ConflictCount: 1, ErrorCategory: "remote_changed",
		Outcomes: []db.SecretSyncItemOutcome{{
			MappingID: 12, AccessKeyID: 17, Mount: "secret", Path: "apps/api",
			Field: "password", Status: db.SecretSyncItemConflict, RemoteVersion: 5,
		}},
	}}
	controller := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{})
	body := []byte(`{"request_id":"manual:api-0001","resolve_operation_id":40}`)
	request := runtimeSecretRequest(http.MethodPost, "/api/project/3/secret_storages/9/sync", body)
	request = helpers.SetContextValue(request, "secretStorage", db.SecretStorage{ID: 9, ProjectID: 3})
	request = helpers.SetContextValue(request, "store", runtimeSecretAPIStore{sync: db.SecretSync{
		ID: 6, ProjectID: 3, StorageID: 9,
	}})
	recorder := httptest.NewRecorder()

	controller.SyncSecrets(recorder, request)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	assert.Equal(t, "manual:api-0001", service.requestID)
	require.NotNil(t, service.resolveID)
	assert.Equal(t, 40, *service.resolveID)
	assert.NotContains(t, recorder.Body.String(), "secret-value")
	assert.Contains(t, recorder.Body.String(), `"remote_version":5`)
}

func TestSecretStorageSyncHistoryIsValueFree(t *testing.T) {
	service := &runtimeSecretStorageServiceStub{history: []db.SecretSyncOperation{{
		ID: 41, Status: db.SecretSyncOperationSucceeded, ChangedCount: 1,
		Outcomes: []db.SecretSyncItemOutcome{{
			MappingID: 12, AccessKeyID: 17, Mount: "secret", Path: "apps/api",
			Field: "password", Status: db.SecretSyncItemChanged,
			ContentFingerprint: "sha256:value-free", RemoteVersion: 6,
		}},
	}}}
	controller := NewSecretStorageController(nil, service, projectRuntimeCapabilityProvider{})
	request := runtimeSecretRequest(
		http.MethodGet, "/api/project/3/secret_storages/9/sync/history?limit=10", nil,
	)
	request = helpers.SetContextValue(request, "secretStorage", db.SecretStorage{ID: 9, ProjectID: 3})
	recorder := httptest.NewRecorder()

	controller.GetSyncHistory(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "sha256:value-free")
	assert.NotContains(t, recorder.Body.String(), "secret-value")
}

func runtimeSecretRequest(method, path string, body []byte) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = helpers.SetContextValue(request, "project", db.Project{ID: 3})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: true})
	request = helpers.SetContextValue(request, "store", runtimeSecretAPIStore{})
	request = helpers.SetContextValue(request, "log_writer", runtimeSecretLogWriter{})
	return request
}
