package api

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
	"strconv"
)

func dockerReconciliationPage(r *http.Request) (db.DockerReconciliationQuery, error) {
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil {
			return db.DockerReconciliationQuery{}, err
		}
	}
	after := int64(0)
	if value := r.URL.Query().Get("after_sequence"); value != "" {
		var err error
		after, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return db.DockerReconciliationQuery{}, err
		}
	}
	query := db.DockerReconciliationQuery{AfterSequence: after, Limit: limit}
	return query, query.Validate()
}

func (c *GlobalRunnerController) GetDockerReconciliationQuarantines(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	boot := r.URL.Query().Get("runner_boot")
	query, err := dockerReconciliationPage(r)
	if err != nil || db.ValidateDockerRunnerBoot(boot) != nil {
		helpers.WriteErrorStatus(w, "invalid Docker reconciliation page", http.StatusBadRequest)
		return
	}
	store, ok := helpers.Store(r).(db.DockerReconciliationRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := store.GetDockerReconciliationPendingStates(db.DockerReconciliationOwner{RunnerID: runner.ID, RunnerBoot: boot}, query)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, page)
}

func (c *GlobalRunnerController) GetDockerReconciliationCandidates(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil {
			helpers.WriteErrorStatus(w, "invalid Docker reconciliation page", http.StatusBadRequest)
			return
		}
	}
	query := db.DockerReconciliationCandidateQuery{AfterFingerprint: r.URL.Query().Get("after_fingerprint"), Limit: limit}
	if query.Validate() != nil {
		helpers.WriteErrorStatus(w, "invalid Docker reconciliation page", http.StatusBadRequest)
		return
	}
	store, ok := helpers.Store(r).(db.DockerReconciliationRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := store.GetDockerReconciliationPendingCandidates(runner.ID, r.URL.Query().Get("session_id"), query)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, page)
}

func (c *GlobalRunnerController) RequestDockerReconciliationRemediation(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	var request db.DockerReconciliationRemediationRequest
	if !helpers.Bind(w, r, &request) {
		return
	}
	store, ok := helpers.Store(r).(db.DockerReconciliationRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	command, err := store.RequestDockerReconciliationRemediation(runner.ID, request)
	if err != nil {
		helpers.WriteErrorStatus(w, "Docker reconciliation remediation request rejected", http.StatusConflict)
		return
	}
	helpers.WriteJSON(w, http.StatusAccepted, command)
}

func (c *GlobalRunnerController) GetDockerExecutionPolicy(w http.ResponseWriter, r *http.Request) {
	policy, err := helpers.Store(r).GetDockerExecutionPolicy()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, policy)
}

func (c *GlobalRunnerController) UpdateDockerExecutionPolicy(w http.ResponseWriter, r *http.Request) {
	var policy db.DockerExecutionPolicy
	if !helpers.Bind(w, r, &policy) {
		return
	}
	saved, err := helpers.Store(r).SaveDockerExecutionPolicy(policy, policy.Revision)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, db.ErrDockerExecutionPolicyRevisionConflict) {
			status = http.StatusConflict
		}
		helpers.WriteErrorStatus(w, err.Error(), status)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, saved)
}

func (c *GlobalRunnerController) TestDockerExecutionPolicy(w http.ResponseWriter, r *http.Request) {
	var request db.DockerExecutionPolicyTestRequest
	if !helpers.Bind(w, r, &request) {
		return
	}
	policy, err := helpers.Store(r).GetDockerExecutionPolicy()
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, policy.Test(request))
}
