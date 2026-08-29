package api

import (
	"net/http"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
)

func readinessHandler(w http.ResponseWriter, r *http.Request) {
	if !util.HAEnabled() {
		helpers.WriteJSON(w, http.StatusOK, pro_interfaces.ClusterServiceReadiness{
			Ready: true, AcceptingCoordinatedWork: true, State: pro_interfaces.ClusterServiceReady,
		})
		return
	}
	provider, ok := helpers.GetFromContext(r, "cluster_inspector").(pro_interfaces.ClusterReadinessProvider)
	if !ok || provider == nil {
		helpers.WriteJSON(w, http.StatusServiceUnavailable, pro_interfaces.ClusterServiceReadiness{
			State:  pro_interfaces.ClusterServiceRegistrationPending,
			Reason: "enhanced cluster readiness is unavailable",
		})
		return
	}
	readiness := provider.Readiness()
	status := http.StatusOK
	if !readiness.Ready {
		status = http.StatusServiceUnavailable
	}
	helpers.WriteJSON(w, status, readiness)
}
