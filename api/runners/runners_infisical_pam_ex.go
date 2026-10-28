package runners

import (
	"net/http"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/services/runners"
	"github.com/semaphoreui/semaphore/services/tasks"
	log "github.com/sirupsen/logrus"
)

// prepareInfisicalPAMDispatch loads the task's Infisical PAM routes on the
// node that serves this poll, since the routes are not persisted with the
// task and another HA node may have started it. It returns false when the
// task was failed and must not be dispatched.
func (c *RunnerController) prepareInfisicalPAMDispatch(tsk *tasks.TaskRunner, runner *db.Runner, header http.Header) bool {
	if err := tsk.RefreshInfisicalPAMForDispatch(); err != nil {
		log.WithError(err).WithFields(log.Fields{
			"runner_id": runner.ID, "task_id": tsk.Task.ID, "context": "infisical_pam",
		}).Error("failed to load Infisical PAM routes")
		tsk.Log("Failed to load Infisical PAM routes for task dispatch. More details in the server logs.")
		tsk.FailDispatch()
		c.taskPool.FinalizeRemoteTask(tsk, runner)
		return false
	}
	if !hasInfisicalPAMRoutes(tsk.Task.InfisicalPAM) {
		return true
	}
	if executor := runner.EffectiveExecutorType(); executor == db.RunnerExecutorDocker || executor == db.RunnerExecutorK8s {
		// Container executors cannot reach the broker; keep the machine
		// identity secret off runners that would not use it.
		tsk.Task.InfisicalPAM = nil
		tsk.Log("Infisical PAM routes are not applied: container tasks cannot reach the runner's Infisical PAM broker")
		return true
	}
	if !runners.RunnerSupportsInfisicalPAM(header) {
		tsk.Log("Runner does not support Infisical PAM routing. Upgrade the runner before retrying the task.")
		tsk.FailDispatch()
		c.taskPool.FinalizeRemoteTask(tsk, runner)
		return false
	}
	return true
}

func hasInfisicalPAMRoutes(configs []db.TaskInfisicalPAM) bool {
	for _, config := range configs {
		if len(config.Routes) > 0 {
			return true
		}
	}
	return false
}

// collectInfisicalPAMAccessKeys stages each machine identity secret, already
// decrypted at task start, for the runner's access key channel.
func collectInfisicalPAMAccessKeys(configs []db.TaskInfisicalPAM, keys map[int]db.AccessKey) {
	for _, config := range configs {
		keys[config.CredentialKeyID] = config.Credential
	}
}
