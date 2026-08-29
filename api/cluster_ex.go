package api

import (
	"encoding/json"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	"net/http"
	"strconv"
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
