package api

import (
	"errors"
	"github.com/semaphoreui/semaphore/api/helpers"
	"github.com/semaphoreui/semaphore/db"
	"net/http"
)

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
