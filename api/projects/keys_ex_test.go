package projects

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"golang.org/x/crypto/ssh"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestGetKeyExposesValueFreeRuntimeSecretReference(t *testing.T) {
	reference := pro_interfaces.SecretReference{
		StorageID: 9, Mount: "team", Path: "apps/api", Version: 2, Field: "password",
	}
	encoded, err := reference.Encode()
	assert.NoError(t, err)
	sourceType := db.AccessKeySourceStorageVault
	request := httptest.NewRequest(http.MethodGet, "/api/project/3/keys/12", nil)
	request = helpers.SetContextValue(request, "accessKey", db.AccessKey{
		ID: 12, Name: "runtime", Type: db.AccessKeyString, ProjectID: intPtr(3),
		SourceStorageID: intPtr(9), SourceStorageType: &sourceType, SourceStorageKey: &encoded,
	})
	recorder := httptest.NewRecorder()

	GetKeys(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var body map[string]any
	assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, "apps/api", body["source_storage_key"])
	assert.Equal(t, "team", body["source_storage_mount"])
	assert.Equal(t, float64(2), body["source_storage_version"])
	assert.Equal(t, "password", body["source_storage_field"])
	assert.NotContains(t, recorder.Body.String(), encoded)
}

func TestUpdateKeyRejectsGenericSecretOverrideForGeneratedSSHKey(t *testing.T) {
	metadata, _, _ := generatedSSHKeyMetadata(t)
	service := &mockAccessKeyService{}
	controller := NewKeyController(service)
	oldKey := db.AccessKey{
		ID: 10, Name: "generated", Type: db.AccessKeySSH, ProjectID: intPtr(1), Plain: &metadata,
	}
	request, recorder := newUpdateKeyRequest(oldKey,
		`{"id":10,"name":"generated","type":"ssh","project_id":1,"override_secret":true}`)

	controller.UpdateKey(recorder, request)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "rotate")
	assert.Empty(t, service.updated)
}

type generatedSSHKeyServiceMock struct {
	mockAccessKeyService
	createRequest server.CreateGeneratedSSHKeyRequest
	rotateRequest server.RotateGeneratedSSHKeyRequest
	result        server.GeneratedSSHKeyResult
	err           error
}

func (m *generatedSSHKeyServiceMock) CreateGeneratedSSHKey(request server.CreateGeneratedSSHKeyRequest) (server.GeneratedSSHKeyResult, error) {
	m.createRequest = request
	return m.result, m.err
}

func (m *generatedSSHKeyServiceMock) RotateGeneratedSSHKey(request server.RotateGeneratedSSHKeyRequest) (server.GeneratedSSHKeyResult, error) {
	m.rotateRequest = request
	return m.result, m.err
}

type generatedSSHKeyAPIStore struct {
	db.Store
	repositories []db.Repository
	keys         []db.AccessKey
	events       []db.Event
}

func (s *generatedSSHKeyAPIStore) GetRepositories(int, db.RetrieveQueryParams) ([]db.Repository, error) {
	return s.repositories, nil
}

func (s *generatedSSHKeyAPIStore) GetAccessKeys(int, db.GetAccessKeyOptions, db.RetrieveQueryParams) ([]db.AccessKey, error) {
	return s.keys, nil
}

func (s *generatedSSHKeyAPIStore) CreateEvent(event db.Event) (db.Event, error) {
	s.events = append(s.events, event)
	return event, nil
}

type generatedSSHKeyLogWriter struct {
	events []pro_interfaces.EventLogRecord
}

func (w *generatedSSHKeyLogWriter) WriteEventLog(event pro_interfaces.EventLogRecord) error {
	w.events = append(w.events, event)
	return nil
}

func (*generatedSSHKeyLogWriter) WriteTaskLog(pro_interfaces.TaskLogRecord) error { return nil }

func (*generatedSSHKeyLogWriter) WriteResult(any) error { return nil }

func generatedSSHKeyMetadata(t *testing.T) (string, string, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(t, err)
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	assert.NoError(t, err)
	public := string(ssh.MarshalAuthorizedKey(publicKey))
	fingerprint := ssh.FingerprintSHA256(publicKey)
	metadata, err := json.Marshal(server.GeneratedSSHKeyMetadata{
		PublicKey: public, Fingerprint: fingerprint, Algorithm: server.GeneratedSSHKeyAlgorithmEd25519,
	})
	assert.NoError(t, err)
	return string(metadata), public, fingerprint
}

func generatedSSHKeyRequestWithContext(method string, body string, store db.Store, writer pro_interfaces.LogWriteService) *http.Request {
	request := httptest.NewRequest(method, "/api/project/1/keys/generate", strings.NewReader(body))
	request = helpers.SetContextValue(request, "project", db.Project{ID: 1})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 9})
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "log_writer", writer)
	return request
}

func TestGeneratedSSHKeyRequestRejectsUnknownAndOversizedBodies(t *testing.T) {
	service := &generatedSSHKeyServiceMock{}
	controller := NewKeyController(service)
	for _, body := range []string{
		`{"name":"key","private_key":"forbidden"}`,
		`{"name":"key","project_id":2}`,
		`{"name":"key","type":"ssh"}`,
		`{"name":"key","source_storage_type":"env"}`,
		`{"name":"` + strings.Repeat("x", generatedSSHKeyRequestMaxBytes) + `"}`,
	} {
		recorder := httptest.NewRecorder()
		controller.GenerateSSHKey(recorder, httptest.NewRequest(http.MethodPost, "/keys/generate", strings.NewReader(body)))
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
		assert.Empty(t, service.createRequest)
	}
}

func TestAccessKeyReadDTORedactsEverySecretFieldAndExposesGeneratedMetadata(t *testing.T) {
	metadata, publicKey, fingerprint := generatedSSHKeyMetadata(t)
	key := db.AccessKey{
		ID: 7, Name: "generated", Type: db.AccessKeySSH, ProjectID: intPtr(1), Plain: &metadata,
		Secret: new("ciphertext"), String: "must-not-leak",
		SshKey:        db.SshKey{Login: "deploy", Passphrase: "passphrase", PrivateKey: "private-key"},
		LoginPassword: db.LoginPassword{Login: "deploy", Password: "password"},
	}
	body, err := json.Marshal(accessKeyReadDTOFrom(key))
	assert.NoError(t, err)
	assert.NotContains(t, string(body), `"private_key":`)
	assert.NotContains(t, string(body), `"passphrase":`)
	assert.NotContains(t, string(body), `"password":`)
	assert.NotContains(t, string(body), "must-not-leak")
	assert.NotContains(t, string(body), "ciphertext")
	assert.Contains(t, string(body), `"ssh":{}`)
	assert.Contains(t, string(body), `"login_password":{}`)
	assert.Contains(t, string(body), `"generated_ssh_key"`)
	var readResponse map[string]any
	assert.NoError(t, json.Unmarshal(body, &readResponse))
	metadataResponse := readResponse["generated_ssh_key"].(map[string]any)
	assert.Equal(t, publicKey, metadataResponse["public_key"])
	assert.Contains(t, string(body), fingerprint)
}

func TestGetKeysHandlersRedactPrivateFieldsAndExposeGeneratedMetadataAfterReopen(t *testing.T) {
	metadata, publicKey, fingerprint := generatedSSHKeyMetadata(t)
	key := db.AccessKey{
		ID: 7, Name: "generated", Type: db.AccessKeySSH, ProjectID: intPtr(1), Plain: &metadata,
		Secret: new("ciphertext"), String: "must-not-leak",
		SshKey:        db.SshKey{Passphrase: "passphrase", PrivateKey: "private-key"},
		LoginPassword: db.LoginPassword{Password: "password"},
	}
	store := &generatedSSHKeyAPIStore{keys: []db.AccessKey{key}}
	assertReadBody := func(body string) {
		assert.NotContains(t, body, `"private_key":`)
		assert.NotContains(t, body, `"passphrase":`)
		assert.NotContains(t, body, `"password":`)
		assert.NotContains(t, body, "ciphertext")
		assert.NotContains(t, body, "must-not-leak")
		assert.Contains(t, body, `"generated_ssh_key"`)
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/project/1/keys", nil)
	listRequest = helpers.SetContextValue(listRequest, "project", db.Project{ID: 1})
	listRequest = helpers.SetContextValue(listRequest, "store", store)
	listRecorder := httptest.NewRecorder()
	GetKeys(listRecorder, listRequest)
	assert.Equal(t, http.StatusOK, listRecorder.Code)
	assertReadBody(listRecorder.Body.String())
	var listResponse []map[string]any
	assert.NoError(t, json.Unmarshal(listRecorder.Body.Bytes(), &listResponse))
	assert.Equal(t, publicKey, listResponse[0]["generated_ssh_key"].(map[string]any)["public_key"])
	assert.Equal(t, fingerprint, listResponse[0]["generated_ssh_key"].(map[string]any)["fingerprint"])

	singleRequest := httptest.NewRequest(http.MethodGet, "/api/project/1/keys/7", nil)
	singleRequest = helpers.SetContextValue(singleRequest, "accessKey", key)
	singleRecorder := httptest.NewRecorder()
	GetKeys(singleRecorder, singleRequest)
	assert.Equal(t, http.StatusOK, singleRecorder.Code)
	assertReadBody(singleRecorder.Body.String())
}

func TestGenerateSSHKeyReturnsOnlyPublicMaterialAndAuditsSuccess(t *testing.T) {
	metadata, publicKey, fingerprint := generatedSSHKeyMetadata(t)
	service := &generatedSSHKeyServiceMock{result: server.GeneratedSSHKeyResult{
		Key: db.AccessKey{
			ID: 7, Name: "generated", Type: db.AccessKeySSH, ProjectID: intPtr(1), Plain: &metadata,
			Secret: new("ciphertext"), SshKey: db.SshKey{PrivateKey: "private-key", Passphrase: "passphrase"},
		},
		PublicKey: publicKey, Fingerprint: fingerprint, Algorithm: server.GeneratedSSHKeyAlgorithmEd25519,
	}}
	store := &generatedSSHKeyAPIStore{}
	writer := &generatedSSHKeyLogWriter{}
	controller := NewKeyController(service)
	recorder := httptest.NewRecorder()
	controller.GenerateSSHKey(recorder, generatedSSHKeyRequestWithContext(http.MethodPost,
		`{"name":"generated","login":"deploy"}`, store, writer))

	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, 1, service.createRequest.ProjectID)
	assert.Equal(t, "generated", service.createRequest.Name)
	assert.Equal(t, "deploy", service.createRequest.Login)
	assert.NotContains(t, recorder.Body.String(), `"private_key":`)
	assert.NotContains(t, recorder.Body.String(), `"passphrase":`)
	assert.NotContains(t, recorder.Body.String(), "ciphertext")
	assert.NotContains(t, recorder.Body.String(), "\"string\"")
	var response generatedSSHKeyResponse
	assert.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, publicKey, response.PublicKey)
	assert.Len(t, store.events, 1)
	assert.NotNil(t, store.events[0].Description)
	assert.Contains(t, *store.events[0].Description, "SSH key generated")
	assert.Contains(t, *store.events[0].Description, fingerprint)
	assert.NotContains(t, *store.events[0].Description, publicKey)
	assert.NotContains(t, *store.events[0].Description, "private-key")
	assert.Len(t, writer.events, 1)
}

func TestGenerateSSHKeyFailureDoesNotCreateEventOrSuccessResponse(t *testing.T) {
	service := &generatedSSHKeyServiceMock{err: errors.New("generation failed")}
	store := &generatedSSHKeyAPIStore{}
	writer := &generatedSSHKeyLogWriter{}
	recorder := httptest.NewRecorder()
	NewKeyController(service).GenerateSSHKey(recorder, generatedSSHKeyRequestWithContext(http.MethodPost,
		`{"name":"generated"}`, store, writer))

	assert.NotEqual(t, http.StatusCreated, recorder.Code)
	assert.Empty(t, store.events)
	assert.Empty(t, writer.events)
	assert.NotContains(t, recorder.Body.String(), `"public_key"`)
}

func TestRotateGeneratedSSHKeyClearsRepositoryCacheAndRequiresManagerPermission(t *testing.T) {
	metadata, publicKey, fingerprint := generatedSSHKeyMetadata(t)
	service := &generatedSSHKeyServiceMock{result: server.GeneratedSSHKeyResult{
		Key:       db.AccessKey{ID: 7, Name: "generated", Type: db.AccessKeySSH, ProjectID: intPtr(1), Plain: &metadata},
		PublicKey: publicKey, Fingerprint: fingerprint, Algorithm: server.GeneratedSSHKeyAlgorithmEd25519,
	}}
	controller := NewKeyController(service)
	projectID := 1
	key := db.AccessKey{ID: 7, Name: "generated", Type: db.AccessKeySSH, ProjectID: &projectID}

	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	t.Cleanup(func() { util.Config = previousConfig })
	repository := db.Repository{ID: 5, ProjectID: projectID, SSHKeyID: key.ID}
	cacheDir := repository.GetFullPath(13)
	assert.NoError(t, os.MkdirAll(cacheDir, 0o700))
	assert.NoError(t, os.WriteFile(cacheDir+"/marker", []byte("cache"), 0o600))

	store := &generatedSSHKeyAPIStore{repositories: []db.Repository{repository}}
	writer := &generatedSSHKeyLogWriter{}
	request := generatedSSHKeyRequestWithContext(http.MethodPost, `{"confirm_rotation":true}`, store, writer)
	request = helpers.SetContextValue(request, "accessKey", key)
	recorder := httptest.NewRecorder()
	controller.RotateGeneratedSSHKey(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.True(t, service.rotateRequest.ConfirmRotation)
	assert.Equal(t, key.ID, service.rotateRequest.KeyID)
	_, err := os.Stat(cacheDir)
	assert.True(t, os.IsNotExist(err))
	assert.Len(t, store.events, 1)
	assert.Contains(t, *store.events[0].Description, "SSH key rotated")

	deniedRequest := generatedSSHKeyRequestWithContext(http.MethodPost, `{"name":"denied"}`, store, writer)
	deniedRequest = helpers.SetContextValue(deniedRequest, "permissions", db.ProjectUserPermission(0))
	deniedRecorder := httptest.NewRecorder()
	GetMustCanMiddleware(db.CanManageProjectResources)(http.HandlerFunc(controller.GenerateSSHKey)).ServeHTTP(deniedRecorder, deniedRequest)
	assert.Equal(t, http.StatusForbidden, deniedRecorder.Code)
}

func TestRotateGeneratedSSHKeyRejectsBeforeInvalidatingRepositoryCache(t *testing.T) {
	service := &generatedSSHKeyServiceMock{}
	controller := NewKeyController(service)
	projectID := 1
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	t.Cleanup(func() { util.Config = previousConfig })

	for _, key := range []db.AccessKey{
		{ID: 7, ProjectID: &projectID, Type: db.AccessKeyString},
		{ID: 7, ProjectID: &projectID, Type: db.AccessKeySSH, Synchronized: true},
		func() db.AccessKey {
			source := db.AccessKeySourceStorageEnv
			return db.AccessKey{ID: 7, ProjectID: &projectID, Type: db.AccessKeySSH, SourceStorageType: &source}
		}(),
	} {
		repository := db.Repository{ID: key.ID, ProjectID: projectID, SSHKeyID: key.ID}
		cacheDir := repository.GetFullPath(13)
		assert.NoError(t, os.MkdirAll(cacheDir, 0o700))
		store := &generatedSSHKeyAPIStore{repositories: []db.Repository{repository}}
		writer := &generatedSSHKeyLogWriter{}
		request := generatedSSHKeyRequestWithContext(http.MethodPost, `{"confirm_rotation":true}`, store, writer)
		request = helpers.SetContextValue(request, "accessKey", key)
		recorder := httptest.NewRecorder()
		controller.RotateGeneratedSSHKey(recorder, request)
		assert.Equal(t, http.StatusBadRequest, recorder.Code)
		_, err := os.Stat(cacheDir)
		assert.NoError(t, err)
		assert.Empty(t, service.rotateRequest)
	}

	key := db.AccessKey{ID: 7, ProjectID: &projectID, Type: db.AccessKeySSH}
	repository := db.Repository{ID: key.ID, ProjectID: projectID, SSHKeyID: key.ID}
	cacheDir := repository.GetFullPath(14)
	assert.NoError(t, os.MkdirAll(cacheDir, 0o700))
	request := generatedSSHKeyRequestWithContext(http.MethodPost, `{"confirm_rotation":false}`, &generatedSSHKeyAPIStore{repositories: []db.Repository{repository}}, &generatedSSHKeyLogWriter{})
	request = helpers.SetContextValue(request, "accessKey", key)
	recorder := httptest.NewRecorder()
	controller.RotateGeneratedSSHKey(recorder, request)
	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	_, err := os.Stat(cacheDir)
	assert.NoError(t, err)
}
