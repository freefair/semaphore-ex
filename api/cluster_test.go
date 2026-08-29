package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	taskServices "github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupClusterTestConfig() {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = nil
}

// poolWithTasks builds a TaskPool backed by an in-memory store pre-populated
// with one queued and one running task.
func poolWithTasks() *taskServices.TaskPool {
	state := taskServices.NewMemoryTaskStateStore()
	state.Enqueue(&taskServices.TaskRunner{
		Task: db.Task{ID: 1, ProjectID: 10, TemplateID: 100},
	})
	state.SetRunning(&taskServices.TaskRunner{
		Task: db.Task{ID: 2, ProjectID: 10, TemplateID: 100},
	})
	return new(taskServices.CreateTaskPool(
		nil,
		state,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	))
}

func TestGetClusterStatus_HADisabled(t *testing.T) {
	setupClusterTestConfig()

	req := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
	w := httptest.NewRecorder()

	getClusterStatus(w, req)

	var body map[string]any
	var bodyBytes = w.Body.Bytes()
	require.NoError(t, json.Unmarshal(bodyBytes, &body))
	assert.Equal(t, false, body["ha_enabled"])
	// No inspector -> no node / redis sections, but never a 500.
	assert.NotContains(t, body, "nodes")
	assert.NotContains(t, body, "redis")
}

func TestGetClusterNodesPaginatesAdminInspectorResults(t *testing.T) {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = &util.HAConfig{Enabled: true}
	inspector := &clusterInspectorFake{nodes: []pro_interfaces.NodeInfo{
		{NodeID: "node-a", BootID: "boot-a"},
		{NodeID: "node-b", BootID: "boot-b"},
		{NodeID: "node-c", BootID: "boot-c"},
	}}
	req := httptest.NewRequest(http.MethodGet, "/api/cluster/nodes?limit=1&offset=1", nil)
	req = helpers.SetContextValue(req, "cluster_inspector", inspector)
	w := httptest.NewRecorder()

	getClusterNodes(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body clusterNodePage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 3, body.Total)
	assert.Equal(t, 1, body.Limit)
	assert.Equal(t, 1, body.Offset)
	require.Len(t, body.Items, 1)
	assert.Equal(t, "boot-b", body.Items[0].BootID)
}

func TestGetClusterStatusAggregatesReadyStaleIncompatibleAndDrainingNodes(t *testing.T) {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = &util.HAConfig{Enabled: true}
	req := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
	req = helpers.SetContextValue(req, "cluster_inspector", &clusterInspectorFake{nodes: []pro_interfaces.NodeInfo{
		{Alive: true, Ready: true, CompatibilityState: pro_interfaces.ClusterNodeCompatible},
		{Alive: false, CompatibilityState: pro_interfaces.ClusterNodeStale},
		{Alive: true, CompatibilityState: pro_interfaces.ClusterNodeIncompatibleSchema},
		{Alive: true, CompatibilityState: pro_interfaces.ClusterNodeDraining},
	}})
	w := httptest.NewRecorder()

	getClusterStatus(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	health := body["health"].(map[string]any)
	assert.Equal(t, float64(4), health["total"])
	assert.Equal(t, float64(1), health["ready"])
	assert.Equal(t, float64(1), health["stale"])
	assert.Equal(t, float64(1), health["incompatible"])
	assert.Equal(t, float64(1), health["draining"])
}

func TestGetClusterNodeReturnsOnlyTheRequestedBootIdentity(t *testing.T) {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = &util.HAConfig{Enabled: true}
	req := httptest.NewRequest(http.MethodGet, "/api/cluster/nodes/boot-b", nil)
	req = mux.SetURLVars(req, map[string]string{"boot_id": "boot-b"})
	req = helpers.SetContextValue(req, "cluster_inspector", &clusterInspectorFake{nodes: []pro_interfaces.NodeInfo{
		{NodeID: "node-a", BootID: "boot-a"}, {NodeID: "node-b", BootID: "boot-b"},
	}})
	w := httptest.NewRecorder()

	getClusterNode(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"boot_id":"boot-b"`)
	assert.NotContains(t, w.Body.String(), `"boot_id":"boot-a"`)
}

func TestSetClusterNodeDrainingUsesAdminInspector(t *testing.T) {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = &util.HAConfig{Enabled: true}
	inspector := &clusterInspectorFake{nodes: []pro_interfaces.NodeInfo{{BootID: "boot-a"}}}
	req := httptest.NewRequest(http.MethodPost, "/api/cluster/nodes/boot-a/draining", strings.NewReader(`{"draining":true}`))
	req = mux.SetURLVars(req, map[string]string{"boot_id": "boot-a"})
	req = helpers.SetContextValue(req, "cluster_inspector", inspector)
	w := httptest.NewRecorder()

	setClusterNodeDraining(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "boot-a", inspector.drainedBootID)
	assert.True(t, inspector.draining)
}

func TestSetClusterNodeDrainingRejectsOmittedState(t *testing.T) {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = &util.HAConfig{Enabled: true}
	inspector := &clusterInspectorFake{}
	req := httptest.NewRequest(http.MethodPost, "/api/cluster/nodes/boot-a/draining", strings.NewReader(`{}`))
	req = mux.SetURLVars(req, map[string]string{"boot_id": "boot-a"})
	req = helpers.SetContextValue(req, "cluster_inspector", inspector)
	w := httptest.NewRecorder()

	setClusterNodeDraining(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Empty(t, inspector.drainedBootID)
}

func TestGetClusterTasks_Snapshot(t *testing.T) {
	setupClusterTestConfig()

	req := httptest.NewRequest(http.MethodGet, "/api/cluster/tasks", nil)
	req = helpers.SetContextValue(req, "task_pool", poolWithTasks())
	w := httptest.NewRecorder()

	getClusterTasks(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var snap taskServices.TaskStateSnapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snap))
	require.Len(t, snap.Queue, 1)
	assert.Equal(t, 1, snap.Queue[0].TaskID)
	require.Len(t, snap.Running, 1)
	assert.Equal(t, 2, snap.Running[0].TaskID)
}

func TestGetClusterTasks_NoPool(t *testing.T) {
	setupClusterTestConfig()

	req := httptest.NewRequest(http.MethodGet, "/api/cluster/tasks", nil)
	w := httptest.NewRecorder()

	getClusterTasks(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var snap taskServices.TaskStateSnapshot
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &snap))
	assert.Empty(t, snap.Queue)
	assert.Empty(t, snap.Running)
}

func TestClearClusterTasks_HonorsScope(t *testing.T) {
	setupClusterTestConfig()

	req := httptest.NewRequest(http.MethodDelete, "/api/cluster/tasks",
		strings.NewReader(`{"scope":{"queue":true}}`))
	req = helpers.SetContextValue(req, "task_pool", poolWithTasks())
	req = helpers.SetContextValue(req, "user", &db.User{Username: "admin"})
	w := httptest.NewRecorder()

	clearClusterTasks(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var res taskServices.ClearResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, 1, res.DeletedKeys)
	assert.Equal(t, 1, res.PerGroup["queue"])
	// Running was not in scope.
	_, hasRunning := res.PerGroup["running"]
	assert.False(t, hasRunning)
}

func TestClearClusterTasks_RejectsEmptyScope(t *testing.T) {
	setupClusterTestConfig()

	req := httptest.NewRequest(http.MethodDelete, "/api/cluster/tasks",
		strings.NewReader(`{"scope":{}}`))
	req = helpers.SetContextValue(req, "task_pool", poolWithTasks())
	req = helpers.SetContextValue(req, "user", &db.User{Username: "admin"})
	w := httptest.NewRecorder()

	clearClusterTasks(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestClearClusterTasks_RejectsMissingBody(t *testing.T) {
	setupClusterTestConfig()

	req := httptest.NewRequest(http.MethodDelete, "/api/cluster/tasks", nil)
	req = helpers.SetContextValue(req, "task_pool", poolWithTasks())
	req = helpers.SetContextValue(req, "user", &db.User{Username: "admin"})
	w := httptest.NewRecorder()

	clearClusterTasks(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

type clusterInspectorFake struct {
	nodes         []pro_interfaces.NodeInfo
	drainedBootID string
	draining      bool
}

func (f clusterInspectorFake) Nodes() ([]pro_interfaces.NodeInfo, error) { return f.nodes, nil }
func (clusterInspectorFake) RedisInfo() (pro_interfaces.RedisInfo, error) {
	return pro_interfaces.RedisInfo{}, nil
}

func (f *clusterInspectorFake) SetNodeDraining(bootID string, draining bool) error {
	f.drainedBootID = bootID
	f.draining = draining
	return nil
}
