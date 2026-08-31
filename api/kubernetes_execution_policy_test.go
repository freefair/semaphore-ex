package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db/sql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKubernetesExecutionPolicyAdminEndpointsAreAliasScopedAndFenced(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	controller := NewGlobalRunnerController(nil)
	policy := sqlPolicyFixture("qa-cluster")
	body, err := json.Marshal(policy)
	require.NoError(t, err)
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodPut, "/api/runners/kubernetes-policies/qa-cluster", bytes.NewReader(body)), "store", store)
	request = mux.SetURLVars(request, map[string]string{"cluster_alias": "qa-cluster"})
	recorder := httptest.NewRecorder()
	controller.UpdateKubernetesExecutionPolicy(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var saved db.KubernetesExecutionPolicy
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &saved))
	assert.Equal(t, 1, saved.Revision)
	staleBody, _ := json.Marshal(policy)
	stale := helpers.SetContextValue(httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(staleBody)), "store", store)
	stale = mux.SetURLVars(stale, map[string]string{"cluster_alias": "qa-cluster"})
	staleRecorder := httptest.NewRecorder()
	controller.UpdateKubernetesExecutionPolicy(staleRecorder, stale)
	assert.Equal(t, http.StatusConflict, staleRecorder.Code)
	test := helpers.SetContextValue(httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"cluster_alias":"qa-cluster","namespace":"semaphore-jobs","task_image":"registry.example.test/other@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","helper_image":"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","service_account":"semaphore-task","runtime_class":"","network_profile":"deny-all","resources":{"cpu_request_milli":100,"cpu_limit_milli":500,"memory_request_bytes":67108864,"memory_limit_bytes":268435456,"ephemeral_storage_request_bytes":67108864,"ephemeral_storage_limit_bytes":268435456},"terminal_retention_seconds":3600,"volume_types":["emptyDir","secret"],"has_restricted_security_profile":true}`)), "store", store)
	test = mux.SetURLVars(test, map[string]string{"cluster_alias": "qa-cluster"})
	testRecorder := httptest.NewRecorder()
	controller.TestKubernetesExecutionPolicy(testRecorder, test)
	assert.Equal(t, http.StatusOK, testRecorder.Code)
	assert.Contains(t, testRecorder.Body.String(), db.KubernetesPolicyRuleImageDenied)
}

func TestKubernetesExecutionPolicyEndpointsAreAdminOnly(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	controller := NewGlobalRunnerController(nil)
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/", nil), "store", store)
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: false})
	request = mux.SetURLVars(request, map[string]string{"cluster_alias": "qa-cluster"})
	recorder := httptest.NewRecorder()
	adminMiddleware(http.HandlerFunc(controller.GetKubernetesExecutionPolicy)).ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestKubernetesReconciliationDiagnosticsAreAdminOnly(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	controller := NewGlobalRunnerController(nil)
	request := helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/", nil), "store", store)
	request = helpers.SetContextValue(request, "runner", &db.Runner{ID: 7})
	request = helpers.SetContextValue(request, "user", &db.User{ID: 7, Admin: false})
	recorder := httptest.NewRecorder()
	adminMiddleware(http.HandlerFunc(controller.GetKubernetesReconciliationDiagnostics)).ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusForbidden, recorder.Code)

}

func sqlPolicyFixture(alias string) db.KubernetesExecutionPolicy {
	p := db.DefaultKubernetesExecutionPolicy(alias)
	p.AllowedNamespaces = []string{"semaphore-jobs"}
	p.AllowedImages = []string{"registry.example.test/job@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	p.AllowedServiceAccounts = []string{"semaphore-task"}
	p.AllowedRuntimeClasses = []string{""}
	p.AllowedVolumeTypes = []db.KubernetesVolumeType{db.KubernetesVolumeEmptyDir, db.KubernetesVolumeSecret}
	p.AllowedNetworkProfiles = []string{"deny-all"}
	p.NetworkProfile = "deny-all"
	p.NetworkPolicyEnforcement = db.KubernetesNetworkPolicyEnforcementNetworkPolicy
	p.Resources = db.KubernetesExecutionResources{CPURequestMilli: 100, CPULimitMilli: 500, MemoryRequestBytes: 64 << 20, MemoryLimitBytes: 256 << 20, EphemeralStorageRequestBytes: 64 << 20, EphemeralStorageLimitBytes: 256 << 20}
	p.TerminalRetentionSeconds = 3600
	return p
}
