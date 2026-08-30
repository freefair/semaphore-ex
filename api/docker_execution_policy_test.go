package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestDockerReconciliationQuarantineAPIUsesPendingCursorPage(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Now().UTC()
	for _, row := range []struct {
		sequence int64
		status   db.DockerReconciliationQuarantineStatus
	}{{1, db.DockerReconciliationQuarantineRemediated}, {2, db.DockerReconciliationQuarantinePending}, {3, db.DockerReconciliationQuarantinePending}} {
		_, err := store.Sql().Exec(store.PrepareQuery("insert into docker_reconciliation_state (runner_id,runner_boot,project_id,task_id,generation,resource,revision,latest_sequence,container_id,container_name,state,reason,updated_at,quarantine_status,remediation,remediation_reason,quarantined_at,remediated_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), 91, "api-page-boot", 1, row.sequence, 1, db.DockerReconciliationResourceTask, 1, row.sequence, "container", "container", db.DockerReconciliationQuarantine, "", now, row.status, db.DockerReconciliationRemediationRemove, "cleanup", now, now)
		require.NoError(t, err)
	}
	controller := NewGlobalRunnerController(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/runners/91/docker-reconciliation/quarantines?runner_boot=api-page-boot&limit=1", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "runner", &db.Runner{ID: 91})
	recorder := httptest.NewRecorder()
	controller.GetDockerReconciliationQuarantines(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	var page db.DockerReconciliationStatePage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
	require.Len(t, page.States, 1)
	assert.Equal(t, int64(2), page.States[0].LatestSequence)
	require.NotNil(t, page.NextCursor)
}

func TestDockerRemediationCommandJSONPreservesOpaqueCandidateIdentity(t *testing.T) {
	command := db.DockerReconciliationRemediationCommand{CommandID: strings.Repeat("a", 64), SessionID: "session", RunnerID: 7, Action: db.DockerReconciliationRemediationRetryStopAndCleanup, Target: db.DockerReconciliationRemediationTargetCandidate, Fingerprint: strings.Repeat("b", 64), DaemonID: "volume-name", CandidateResource: db.DockerReconciliationCandidateVolume, CandidateIdentity: strings.Repeat("c", 64)}
	payload, err := json.Marshal(command)
	require.NoError(t, err)
	assert.Contains(t, string(payload), `"candidate_identity"`)
	var decoded db.DockerReconciliationRemediationCommand
	require.NoError(t, json.Unmarshal(payload, &decoded))
	assert.Equal(t, command.CandidateIdentity, decoded.CandidateIdentity)
	require.NoError(t, decoded.Validate())
	decoded.CandidateIdentity = strings.Repeat("C", 64)
	assert.Error(t, decoded.Validate())
}

func TestDockerReconciliationDiagnosticsAPIIsAdminOnlyAndRedactsInternals(t *testing.T) {
	store := sql.InitConfigCreateTestStore()
	t.Cleanup(store.Close)
	now := time.Now().UTC()
	_, err := store.Sql().Exec(store.PrepareQuery("insert into docker_reconciliation_state (runner_id,runner_boot,project_id,task_id,generation,resource,revision,latest_sequence,container_id,container_name,state,reason,updated_at,quarantine_status,remediation,remediation_reason,quarantined_at) values (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)"), 92, "historic-boot", 4, 5, 1, db.DockerReconciliationResourceTask, 7, 3, "secret-daemon-id", "safe-container", db.DockerReconciliationQuarantine, "raw daemon reason", now, db.DockerReconciliationQuarantinePending, db.DockerReconciliationRemediationInspect, "raw daemon reason", now)
	require.NoError(t, err)
	_, err = store.Sql().Exec(store.PrepareQuery("insert into docker_reconciliation_orphan_candidate (session_id,runner_id,resource,identifier,name,reason,fingerprint,identity,observed_at,revision,status) values (?,?,?,?,?,?,?,?,?,?,?)"), "historic-session", 92, db.DockerReconciliationCandidateContainer, "candidate-daemon-id", "safe-name", db.DockerReconciliationCandidateMalformed, strings.Repeat("a", 64), "secret-identity-hash", now.Add(time.Second), 2, db.DockerReconciliationCandidatePending)
	require.NoError(t, err)
	controller := NewGlobalRunnerController(nil)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/runners/92/docker-reconciliation/diagnostics?limit=10", nil)
	request = helpers.SetContextValue(request, "store", store)
	request = helpers.SetContextValue(request, "runner", &db.Runner{ID: 92})
	recorder := httptest.NewRecorder()
	controller.GetDockerReconciliationDiagnostics(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	for _, forbidden := range []string{"secret-daemon-id", "candidate-daemon-id", "raw daemon reason", "secret-identity-hash", "fence", "labels", "command_id", "daemon_id", "candidate_identity", "identifier"} {
		assert.NotContains(t, recorder.Body.String(), forbidden)
	}
	var response dockerReconciliationDiagnosticsResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Diagnostics, 2)
	var candidate *dockerReconciliationDiagnosticDTO
	for index := range response.Diagnostics {
		if response.Diagnostics[index].Type == db.DockerReconciliationRemediationTargetCandidate {
			candidate = &response.Diagnostics[index]
		}
	}
	require.NotNil(t, candidate)
	require.NotNil(t, candidate.Remediation.Candidate)
	assert.Equal(t, "historic-session", candidate.Remediation.Candidate.SessionID)
	assert.Equal(t, strings.Repeat("a", 64), candidate.Remediation.Candidate.Fingerprint)

	denied := helpers.SetContextValue(httptest.NewRequest(http.MethodGet, "/api/admin/runners/92/docker-reconciliation/diagnostics", nil), "store", store)
	denied = helpers.SetContextValue(denied, "runner", &db.Runner{ID: 92})
	denied = helpers.SetContextValue(denied, "user", &db.User{ID: 12, Admin: false})
	deniedRecorder := httptest.NewRecorder()
	adminMiddleware(http.HandlerFunc(controller.GetDockerReconciliationDiagnostics)).ServeHTTP(deniedRecorder, denied)
	assert.Equal(t, http.StatusForbidden, deniedRecorder.Code)
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
