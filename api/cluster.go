package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	taskServices "github.com/semaphoreui/semaphore/services/tasks"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

const defaultClusterNodePageSize = 20
const maxClusterNodePageSize = 100

type clusterNodePage struct {
	Items  []pro_interfaces.NodeInfo `json:"items"`
	Total  int                       `json:"total"`
	Limit  int                       `json:"limit"`
	Offset int                       `json:"offset"`
}

type clusterHealthSummary struct {
	Total        int `json:"total"`
	Ready        int `json:"ready"`
	Stale        int `json:"stale"`
	Incompatible int `json:"incompatible"`
	Draining     int `json:"draining"`
}

// clusterInspectorFromContext returns the ClusterInspector injected by the
// router middleware, or nil if HA is disabled / the overlay is absent.
func clusterInspectorFromContext(r *http.Request) pro_interfaces.ClusterInspector {
	ci, _ := helpers.GetFromContext(r, "cluster_inspector").(pro_interfaces.ClusterInspector)
	return ci
}

// taskStateInspectorFromContext returns the TaskStateInspector for the running
// task pool, or nil if the store does not support introspection.
func taskStateInspectorFromContext(r *http.Request) taskServices.TaskStateInspector {
	pool, ok := helpers.GetFromContext(r, "task_pool").(*taskServices.TaskPool)
	if !ok || pool == nil {
		return nil
	}
	inspector, _ := pool.StateStore().(taskServices.TaskStateInspector)
	return inspector
}

// getClusterStatus reports HA mode, cluster membership and Redis stats.
// It never fails: with HA disabled or no inspector it returns ha_enabled:false.
func getClusterStatus(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"ha_enabled": util.HAEnabled(),
	}

	if !util.HAEnabled() {
		helpers.WriteJSON(w, http.StatusOK, body)
		return
	}

	if util.Config.HA != nil {
		body["node_id"] = util.Config.HA.NodeID
	}

	ci := clusterInspectorFromContext(r)

	if ci == nil {
		helpers.WriteErrorStatus(w, "cluster inspection is unavailable (HA mode disabled or overlay missing)", http.StatusServiceUnavailable)
		return
	}

	if nodes, err := ci.Nodes(); err != nil {
		log.WithError(err).Error("cluster: failed to list nodes")
	} else {
		body["nodes"] = nodes
		body["health"] = summarizeClusterHealth(nodes)
	}

	if redisInfo, err := ci.RedisInfo(); err != nil {
		log.WithError(err).Error("cluster: failed to read redis info")
		body["redis"] = redisInfo
	} else {
		body["redis"] = redisInfo
	}
	body["coordinator"] = ci.CoordinatorHealth()

	helpers.WriteJSON(w, http.StatusOK, body)
}

func summarizeClusterHealth(nodes []pro_interfaces.NodeInfo) clusterHealthSummary {
	summary := clusterHealthSummary{Total: len(nodes)}
	for _, node := range nodes {
		switch {
		case node.Ready:
			summary.Ready++
		case !node.Alive || node.CompatibilityState == pro_interfaces.ClusterNodeStale:
			summary.Stale++
		case node.CompatibilityState == pro_interfaces.ClusterNodeDraining:
			summary.Draining++
		default:
			summary.Incompatible++
		}
	}
	return summary
}

func getClusterNodes(w http.ResponseWriter, r *http.Request) {
	ci := clusterInspectorFromContext(r)
	if !util.HAEnabled() || ci == nil {
		helpers.WriteErrorStatus(w, "cluster inspection is unavailable", http.StatusServiceUnavailable)
		return
	}
	nodes, err := ci.Nodes()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	limit := boundedClusterNodeQuery(r.URL.Query().Get("limit"), defaultClusterNodePageSize, maxClusterNodePageSize)
	offset := boundedClusterNodeQuery(r.URL.Query().Get("offset"), 0, len(nodes))
	if offset > len(nodes) {
		offset = len(nodes)
	}
	end := offset + limit
	if end > len(nodes) {
		end = len(nodes)
	}
	helpers.WriteJSON(w, http.StatusOK, clusterNodePage{Items: nodes[offset:end], Total: len(nodes), Limit: limit, Offset: offset})
}

func getClusterNode(w http.ResponseWriter, r *http.Request) {
	ci := clusterInspectorFromContext(r)
	if !util.HAEnabled() || ci == nil {
		helpers.WriteErrorStatus(w, "cluster inspection is unavailable", http.StatusServiceUnavailable)
		return
	}
	bootID, err := helpers.GetStrParam("boot_id", w, r)
	if err != nil {
		return
	}
	nodes, err := ci.Nodes()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	for _, node := range nodes {
		if node.BootID == bootID {
			helpers.WriteJSON(w, http.StatusOK, node)
			return
		}
	}
	helpers.WriteErrorStatus(w, "cluster node not found", http.StatusNotFound)
}

type clusterNodeDrainRequest struct {
	Draining *bool `json:"draining"`
}

func setClusterNodeDraining(w http.ResponseWriter, r *http.Request) {
	ci := clusterInspectorFromContext(r)
	if !util.HAEnabled() || ci == nil {
		helpers.WriteErrorStatus(w, "cluster inspection is unavailable", http.StatusServiceUnavailable)
		return
	}
	bootID, err := helpers.GetStrParam("boot_id", w, r)
	if err != nil {
		return
	}
	var request clusterNodeDrainRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Draining == nil {
		helpers.WriteErrorStatus(w, "draining state is required", http.StatusBadRequest)
		return
	}
	if err := ci.SetNodeDraining(bootID, *request.Draining); err != nil {
		helpers.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func boundedClusterNodeQuery(raw string, fallback int, max int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

// getClusterTasks returns a snapshot of the task pool records (queue, running,
// active, aliases, claims). Works in both HA and non-HA mode.
func getClusterTasks(w http.ResponseWriter, r *http.Request) {
	inspector := taskStateInspectorFromContext(r)
	if inspector == nil {
		helpers.WriteJSON(w, http.StatusOK, taskServices.TaskStateSnapshot{
			Queue:        []taskServices.TaskRecord{},
			Running:      []taskServices.TaskRecord{},
			ActiveByProj: map[int][]taskServices.TaskRecord{},
			Aliases:      map[string]int{},
			Claims:       []int{},
		})
		return
	}

	helpers.WriteJSON(w, http.StatusOK, inspector.Snapshot())
}

// clearClusterTasksRequest is the body of DELETE /api/cluster/tasks.
type clearClusterTasksRequest struct {
	Scope taskServices.ClearScope `json:"scope"`
}

// clearClusterTasks removes the selected task record groups from the backend.
// This is a maintenance action for recovering from a stuck cluster state.
func clearClusterTasks(w http.ResponseWriter, r *http.Request) {
	var req clearClusterTasksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		helpers.WriteErrorStatus(w, "an explicit scope is required", http.StatusBadRequest)
		return
	}

	scope := req.Scope
	if !scope.Queue &&
		!scope.Running &&
		!scope.Active &&
		!scope.Aliases &&
		!scope.Claims &&
		!scope.RuntimeFields {
		helpers.WriteErrorStatus(w, "no record groups selected", http.StatusBadRequest)
		return
	}

	inspector := taskStateInspectorFromContext(r)
	if inspector == nil {
		helpers.WriteErrorStatus(w, "task state store does not support clearing", http.StatusNotImplemented)
		return
	}

	result, err := inspector.ClearTasks(scope)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	user := helpers.UserFromContext(r)
	log.WithFields(log.Fields{
		"context":      "cluster",
		"user":         user.Username,
		"scope":        scope,
		"deleted_keys": result.DeletedKeys,
		"per_group":    result.PerGroup,
	}).Info("cluster tasks cleared from backend")

	helpers.WriteJSON(w, http.StatusOK, result)
}
