package api

import (
	"errors"
	"github.com/gorilla/mux"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
	"strconv"
	"time"
)

type dockerReconciliationDiagnosticsResponse struct {
	Diagnostics []dockerReconciliationDiagnosticDTO `json:"diagnostics"`
	NextCursor  string                              `json:"next_cursor,omitempty"`
}

type kubernetesReconciliationDiagnosticsResponse struct {
	Diagnostics []db.KubernetesReconciliationDiagnostic `json:"diagnostics"`
}

// dockerReconciliationDiagnosticDTO is deliberately separate from db JSON.
// It is the browser's safe, actionable view and excludes daemon identity,
// labels, raw runner reasons, fences, and remediation command internals.
type dockerReconciliationDiagnosticDTO struct {
	Type        db.DockerReconciliationRemediationTarget `json:"type"`
	ObservedAt  time.Time                                `json:"observed_at"`
	Quarantine  *dockerReconciliationQuarantineDTO       `json:"quarantine,omitempty"`
	Candidate   *dockerReconciliationCandidateDTO        `json:"candidate,omitempty"`
	Remediation dockerReconciliationTargetDTO            `json:"remediation"`
}

type dockerReconciliationQuarantineDTO struct {
	RunnerBoot    string                          `json:"runner_boot"`
	ProjectID     int                             `json:"project_id"`
	TaskID        int                             `json:"task_id"`
	Generation    int                             `json:"generation"`
	Resource      db.DockerReconciliationResource `json:"resource"`
	State         db.DockerReconciliationState    `json:"state"`
	ContainerName string                          `json:"container_name,omitempty"`
}

type dockerReconciliationCandidateDTO struct {
	Resource db.DockerReconciliationCandidateResource `json:"resource"`
	Name     string                                   `json:"name,omitempty"`
	Reason   db.DockerReconciliationCandidateReason   `json:"reason"`
}

type dockerReconciliationTargetDTO struct {
	ExpectedRevision int64                                    `json:"expected_revision"`
	Target           db.DockerReconciliationRemediationTarget `json:"target"`
	Quarantine       *db.DockerReconciliationKey              `json:"quarantine,omitempty"`
	Candidate        *dockerReconciliationCandidateTargetDTO  `json:"candidate,omitempty"`
}

type dockerReconciliationCandidateTargetDTO struct {
	SessionID   string `json:"session_id"`
	Fingerprint string `json:"fingerprint"`
}

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

func (c *GlobalRunnerController) GetDockerReconciliationDiagnostics(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		var err error
		limit, err = strconv.Atoi(value)
		if err != nil {
			helpers.WriteErrorStatus(w, "invalid Docker reconciliation diagnostics page", http.StatusBadRequest)
			return
		}
	}
	store, ok := helpers.Store(r).(db.DockerReconciliationRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Docker reconciliation storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	page, err := store.GetDockerReconciliationPendingDiagnostics(runner.ID, db.DockerReconciliationDiagnosticsQuery{Cursor: r.URL.Query().Get("cursor"), Limit: limit})
	if err != nil {
		helpers.WriteErrorStatus(w, "invalid Docker reconciliation diagnostics page", http.StatusBadRequest)
		return
	}
	response := dockerReconciliationDiagnosticsResponse{Diagnostics: make([]dockerReconciliationDiagnosticDTO, 0, len(page.Records)), NextCursor: page.NextCursor}
	for _, record := range page.Records {
		diagnostic := dockerReconciliationDiagnosticDTO{Type: record.Kind, ObservedAt: record.SortAt, Remediation: dockerReconciliationTargetDTO{ExpectedRevision: record.Revision, Target: record.Kind}}
		if record.Kind == db.DockerReconciliationRemediationTargetQuarantine {
			key := db.DockerReconciliationKey{DockerReconciliationOwner: db.DockerReconciliationOwner{RunnerID: runner.ID, RunnerBoot: record.RunnerBoot}, ProjectID: record.ProjectID, TaskID: record.TaskID, Generation: record.Generation, Resource: record.Resource}
			diagnostic.Quarantine = &dockerReconciliationQuarantineDTO{RunnerBoot: record.RunnerBoot, ProjectID: record.ProjectID, TaskID: record.TaskID, Generation: record.Generation, Resource: record.Resource, State: record.State, ContainerName: record.ContainerName}
			diagnostic.Remediation.Quarantine = &key
		} else {
			diagnostic.Candidate = &dockerReconciliationCandidateDTO{Resource: record.CandidateResource, Name: record.CandidateName, Reason: record.CandidateReason}
			diagnostic.Remediation.Candidate = &dockerReconciliationCandidateTargetDTO{SessionID: record.CandidateSessionID, Fingerprint: record.CandidateFingerprint}
		}
		response.Diagnostics = append(response.Diagnostics, diagnostic)
	}
	helpers.WriteJSON(w, http.StatusOK, response)
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

func (c *GlobalRunnerController) GetKubernetesReconciliationDiagnostics(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			helpers.WriteErrorStatus(w, "invalid Kubernetes reconciliation diagnostics page", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	store, ok := helpers.Store(r).(db.KubernetesReconciliationRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Kubernetes reconciliation storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	diagnostics, err := store.GetKubernetesReconciliationPendingDiagnostics(runner.ID, limit)
	if err != nil {
		helpers.WriteErrorStatus(w, "invalid Kubernetes reconciliation diagnostics page", http.StatusBadRequest)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, kubernetesReconciliationDiagnosticsResponse{Diagnostics: diagnostics})
}

func (c *GlobalRunnerController) RequestKubernetesReconciliationRemediation(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)
	var request db.KubernetesReconciliationRemediationRequest
	if !helpers.Bind(w, r, &request) {
		return
	}
	store, ok := helpers.Store(r).(db.KubernetesReconciliationRepository)
	if !ok {
		helpers.WriteErrorStatus(w, "Kubernetes reconciliation storage is unavailable", http.StatusServiceUnavailable)
		return
	}
	command, err := store.RequestKubernetesReconciliationRemediation(runner.ID, request)
	if err != nil {
		helpers.WriteErrorStatus(w, "Kubernetes reconciliation remediation request rejected", http.StatusConflict)
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

func kubernetesPolicyAlias(r *http.Request) (string, error) {
	alias := mux.Vars(r)["cluster_alias"]
	if err := db.ValidateKubernetesClusterAlias(alias); err != nil {
		return "", err
	}
	return alias, nil
}

func (c *GlobalRunnerController) GetKubernetesExecutionPolicy(w http.ResponseWriter, r *http.Request) {
	alias, err := kubernetesPolicyAlias(r)
	if err != nil {
		helpers.WriteErrorStatus(w, "Invalid Kubernetes cluster alias", http.StatusBadRequest)
		return
	}
	policy, err := helpers.Store(r).GetKubernetesExecutionPolicy(alias)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, policy)
}

func (c *GlobalRunnerController) UpdateKubernetesExecutionPolicy(w http.ResponseWriter, r *http.Request) {
	alias, err := kubernetesPolicyAlias(r)
	if err != nil {
		helpers.WriteErrorStatus(w, "Invalid Kubernetes cluster alias", http.StatusBadRequest)
		return
	}
	var policy db.KubernetesExecutionPolicy
	if !helpers.Bind(w, r, &policy) {
		return
	}
	if policy.ClusterAlias != alias {
		helpers.WriteErrorStatus(w, "Kubernetes policy cluster alias does not match route", http.StatusBadRequest)
		return
	}
	saved, err := helpers.Store(r).SaveKubernetesExecutionPolicy(policy, policy.Revision)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, db.ErrKubernetesExecutionPolicyRevisionConflict) {
			status = http.StatusConflict
		}
		helpers.WriteErrorStatus(w, err.Error(), status)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, saved)
}

func (c *GlobalRunnerController) TestKubernetesExecutionPolicy(w http.ResponseWriter, r *http.Request) {
	alias, err := kubernetesPolicyAlias(r)
	if err != nil {
		helpers.WriteErrorStatus(w, "Invalid Kubernetes cluster alias", http.StatusBadRequest)
		return
	}
	var request db.KubernetesExecutionPolicyTestRequest
	if !helpers.Bind(w, r, &request) {
		return
	}
	if request.ClusterAlias != alias {
		helpers.WriteErrorStatus(w, "Kubernetes policy test cluster alias does not match route", http.StatusBadRequest)
		return
	}
	policy, err := helpers.Store(r).GetKubernetesExecutionPolicy(alias)
	if err != nil {
		helpers.WriteError(w, err)
		return
	}
	helpers.WriteJSON(w, http.StatusOK, policy.Test(request))
}
