package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerExecutionPolicyAdminEndpointsUseRevisionFence(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	controller := NewGlobalRunnerController(nil)
	policy := db.DefaultDockerExecutionPolicy()
	policy.AllowedImages = []string{"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	body, err := json.Marshal(policy)
	require.NoError(t, err)
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPut, "/api/admin/runners/docker-policy", bytes.NewReader(body)), "store", store)
	recorder := httptest.NewRecorder()
	controller.UpdateDockerExecutionPolicy(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var saved db.DockerExecutionPolicy
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &saved))
	assert.Equal(t, 1, saved.Revision)

	staleBody, err := json.Marshal(policy)
	require.NoError(t, err)
	staleRequest := helpers.SetContextValue(httptest.NewRequest(http.MethodPut, "/api/admin/runners/docker-policy", bytes.NewReader(staleBody)), "store", store)
	staleRecorder := httptest.NewRecorder()
	controller.UpdateDockerExecutionPolicy(staleRecorder, staleRequest)
	assert.Equal(t, http.StatusConflict, staleRecorder.Code)

	testRequest := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/api/admin/runners/docker-policy/test", bytes.NewBufferString(`{"image":"registry.example.test/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","network":"none","user":"65534:0","nano_cpus":1000000000,"memory_bytes":536870912,"pids_limit":256,"read_only_rootfs":true}`)), "store", store)
	testRecorder := httptest.NewRecorder()
	controller.TestDockerExecutionPolicy(testRecorder, testRequest)
	assert.Equal(t, http.StatusOK, testRecorder.Code)
	assert.Contains(t, testRecorder.Body.String(), db.DockerPolicyRuleImageDenied)
}

func TestDockerExecutionPolicyEndpointsRemainGlobalAdminOnly(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	controller := NewGlobalRunnerController(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/runners/docker-policy", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 99, Admin: false})
	recorder := httptest.NewRecorder()
	adminMiddleware(http.HandlerFunc(controller.GetDockerExecutionPolicy)).ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}
