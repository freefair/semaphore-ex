package api

import (
	"encoding/json"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestGetClusterStatusKeepsCoordinatorAndDisconnectedRedisVisibleDuringOutage(t *testing.T) {
	if util.Config == nil {
		util.Config = &util.ConfigType{}
	}
	util.Config.HA = &util.HAConfig{Enabled: true}
	inspector := clusterInspectorRedisOutageFake{}
	req := httptest.NewRequest(http.MethodGet, "/api/cluster", nil)
	req = helpers.SetContextValue(req, "cluster_inspector", inspector)
	w := httptest.NewRecorder()

	getClusterStatus(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "degraded", body["coordinator"].(map[string]any)["live_events"])
	assert.Equal(t, false, body["redis"].(map[string]any)["connected"])
	assert.Equal(t, "127.0.0.1:16341", body["redis"].(map[string]any)["addr"])
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

type clusterInspectorFake struct {
	nodes         []pro_interfaces.NodeInfo
	drainedBootID string
	draining      bool
}

type clusterInspectorRedisOutageFake struct{}

func (clusterInspectorRedisOutageFake) Nodes() ([]pro_interfaces.NodeInfo, error) {
	return []pro_interfaces.NodeInfo{{NodeID: "node-a", Alive: false, CompatibilityState: pro_interfaces.ClusterNodeStale}}, nil
}

func (clusterInspectorRedisOutageFake) RedisInfo() (pro_interfaces.RedisInfo, error) {
	return pro_interfaces.RedisInfo{Addr: "127.0.0.1:16341", Connected: false}, assert.AnError
}

func (clusterInspectorRedisOutageFake) CoordinatorHealth() pro_interfaces.ClusterCoordinatorHealth {
	return pro_interfaces.ClusterCoordinatorHealth{SQLAuthoritative: true, LiveEvents: "degraded"}
}

func (clusterInspectorRedisOutageFake) SetNodeDraining(string, bool) error { return nil }

func (f clusterInspectorFake) Nodes() ([]pro_interfaces.NodeInfo, error) { return f.nodes, nil }

func (clusterInspectorFake) RedisInfo() (pro_interfaces.RedisInfo, error) {
	return pro_interfaces.RedisInfo{}, nil
}

func (clusterInspectorFake) CoordinatorHealth() pro_interfaces.ClusterCoordinatorHealth {
	return pro_interfaces.ClusterCoordinatorHealth{SQLAuthoritative: true, LiveEvents: "healthy"}
}

func (f *clusterInspectorFake) SetNodeDraining(bootID string, draining bool) error {
	f.drainedBootID = bootID
	f.draining = draining
	return nil
}
