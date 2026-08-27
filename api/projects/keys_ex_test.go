package projects

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
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
