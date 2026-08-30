package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

type runnerWithToken struct {
	db.Runner
	Token string `json:"token"`
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

// GlobalRunnerController handles CRUD for global (non-project) runners.
type GlobalRunnerController struct {
	runnerService server.RunnerService
}

func NewGlobalRunnerController(runnerService server.RunnerService) *GlobalRunnerController {
	return &GlobalRunnerController{
		runnerService: runnerService,
	}
}

func (c *GlobalRunnerController) GetRunners(w http.ResponseWriter, r *http.Request) {
	runners, err := helpers.Store(r).GetAllRunners(false, false, db.RunnerFilterIgnoreTags, nil)

	if err != nil {
		panic(err)
	}

	var result = make([]db.Runner, 0)

	result = append(result, runners...)

	now := tz.Now()
	offlineTimeout := util.Config.RunnersOfflineTimeout()

	for i := range result {
		result[i].Registered = result[i].IsRegistered()
		result[i].FillStatus(now, offlineTimeout)
	}

	helpers.WriteJSON(w, http.StatusOK, result)
}

func (c *GlobalRunnerController) AddRunner(w http.ResponseWriter, r *http.Request) {
	var runner db.Runner
	if !helpers.Bind(w, r, &runner) {
		return
	}

	runner.ProjectID = nil

	newRunner, err := c.runnerService.CreateRunner(runner)

	if err != nil {
		log.Warn("Runner is not created: " + err.Error())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	helpers.WriteJSON(w, http.StatusCreated, runnerWithToken{
		Runner: newRunner,
		Token:  newRunner.Token,
	})
}

func (c *GlobalRunnerController) RunnerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		runnerID, ok := helpers.GetIntParamOrAbort("runner_id", w, r)

		if !ok {
			return
		}

		store := helpers.Store(r)

		runner, err := store.GetGlobalRunner(runnerID)

		if err != nil {
			helpers.WriteJSON(w, http.StatusNotFound, map[string]string{
				"error": "Runner not found",
			})
			return
		}

		r = helpers.SetContextValue(r, "runner", &runner)
		next.ServeHTTP(w, r)
	})
}

func (c *GlobalRunnerController) GetRunner(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)

	runner.Registered = runner.IsRegistered()
	runner.FillStatus(tz.Now(), util.Config.RunnersOfflineTimeout())

	helpers.WriteJSON(w, http.StatusOK, runner)
}

func (c *GlobalRunnerController) UpdateRunner(w http.ResponseWriter, r *http.Request) {
	oldRunner := helpers.GetFromContext(r, "runner").(*db.Runner)

	var runner db.Runner
	if !helpers.Bind(w, r, &runner) {
		return
	}

	store := helpers.Store(r)

	runner.ID = oldRunner.ID
	runner.ProjectID = nil

	err := store.UpdateRunner(runner)

	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *GlobalRunnerController) ClearRunnerCache(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)

	store := helpers.Store(r)

	err := store.ClearRunnerCache(*runner)

	if err != nil {
		helpers.WriteError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *GlobalRunnerController) DeleteRunner(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)

	store := helpers.Store(r)

	err := store.DeleteGlobalRunner(runner.ID)

	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (c *GlobalRunnerController) RegenerateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)

	token, err := c.runnerService.RegenerateRegistrationToken(*runner)

	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, map[string]any{
		"registration_token": token,
		"runner_id":          runner.ID,
	})
}

func (c *GlobalRunnerController) GetRunnerTags(w http.ResponseWriter, r *http.Request) {
	tags, err := helpers.Store(r).GetGlobalRunnerTags()

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	helpers.WriteJSON(w, http.StatusOK, tags)
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

func (c *GlobalRunnerController) SetRunnerActive(w http.ResponseWriter, r *http.Request) {
	runner := helpers.GetFromContext(r, "runner").(*db.Runner)

	store := helpers.Store(r)

	var body struct {
		Active bool `json:"active"`
	}

	if !helpers.Bind(w, r, &body) {
		helpers.WriteErrorStatus(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	runner.Active = body.Active

	err := store.UpdateRunner(*runner)

	if err != nil {
		helpers.WriteErrorStatus(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
