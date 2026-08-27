package projects

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func intPtr(v int) *int { return &v }

// newUpdateKeyRequest builds a request with the URL-resolved oldKey already in
// context (as KeyMiddleware would set it) and the given JSON body.
func newUpdateKeyRequest(oldKey db.AccessKey, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodPut, "/api/project/1/keys/1", strings.NewReader(body))
	req = helpers.SetContextValue(req, "accessKey", oldKey)
	return req, httptest.NewRecorder()
}

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

func TestUpdateKey_RejectsBodyIDMismatch(t *testing.T) {

	svc := &mockAccessKeyService{}
	ctrl := NewKeyController(svc)

	oldKey := db.AccessKey{ID: 10, ProjectID: intPtr(1)}
	// Body targets a different key id than the one resolved from the URL.
	body := `{"id":42,"name":"x","type":"none","project_id":1}`
	req, w := newUpdateKeyRequest(oldKey, body)

	ctrl.UpdateKey(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "must be the same")
	assert.Empty(t, svc.updated, "store must not be touched on rejection")
}

func TestUpdateKey_RejectsCrossProjectMove(t *testing.T) {
	svc := &mockAccessKeyService{}
	ctrl := NewKeyController(svc)

	oldKey := db.AccessKey{ID: 10, ProjectID: intPtr(1)}
	// Body keeps the same key id but points project_id at a foreign project.
	body := `{"id":10,"name":"x","type":"none","project_id":999}`
	req, w := newUpdateKeyRequest(oldKey, body)

	ctrl.UpdateKey(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "other project")
	assert.Empty(t, svc.updated, "store must not be touched on rejection")
}

func TestUpdateKey_RejectsNilBodyProjectID(t *testing.T) {
	svc := &mockAccessKeyService{}
	ctrl := NewKeyController(svc)

	oldKey := db.AccessKey{ID: 10, ProjectID: intPtr(1)}
	// Body omits project_id entirely.
	body := `{"id":10,"name":"x","type":"none"}`
	req, w := newUpdateKeyRequest(oldKey, body)

	ctrl.UpdateKey(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "other project")
	assert.Empty(t, svc.updated, "store must not be touched on rejection")
}
