package tasks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/util"
)

// ErrAllRunnersBusy is returned when all available runners are busy. Used for logic
var ErrAllRunnersBusy = errors.New("all runners busy")

type RemoteJob struct {
	RunnerTag          *string
	RunnerTags         []string
	RunnerTagMatchMode db.RunnerTagMatchMode
	ExecutorImage      *string
	Task               db.Task
	taskPool           *TaskPool
	killed             bool
}

type runnerWebhookPayload struct {
	Action     string `json:"action"`
	ProjectID  int    `json:"project_id"`
	TaskID     int    `json:"task_id"`
	TemplateID int    `json:"template_id"`
	RunnerID   int    `json:"runner_id"`
}

func callRunnerWebhook(runner *db.Runner, tsk *TaskRunner, action string) (err error) {
	if runner.Webhook == "" {
		return
	}

	var jsonBytes []byte
	jsonBytes, err = json.Marshal(runnerWebhookPayload{
		Action:     action,
		ProjectID:  tsk.Task.ProjectID,
		TaskID:     tsk.Task.ID,
		TemplateID: tsk.Template.ID,
		RunnerID:   runner.ID,
	})
	if err != nil {
		return
	}

	// Bound the dispatch: a webhook that connects but never responds would
	// otherwise keep the dispatch goroutine alive forever, hanging the task in
	// "starting" with no runner assigned.
	client := &http.Client{Timeout: 30 * time.Second}

	var req *http.Request
	req, err = http.NewRequest("POST", runner.Webhook, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")

	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return
	}

	if resp != nil {
		defer resp.Body.Close() //nolint:errcheck
	}

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		err = fmt.Errorf("webhook returned incorrect status")
		return
	}

	return
}

func (t *RemoteJob) Run(username string, incomingVersion *string, alias string) (err error) {
	tsk, err := t.taskPool.GetTask(t.Task.ID)

	if err != nil {
		return
	}

	if tsk == nil {
		return fmt.Errorf("task not found")
	}

	tsk.IncomingVersion = incomingVersion
	tsk.Username = username
	tsk.Alias = alias
	t.taskPool.state.UpdateRuntimeFields(tsk)

	requestedTags := db.NormalizeRunnerTags(t.RunnerTags)
	if len(requestedTags) == 0 && t.RunnerTag != nil {
		requestedTags = db.NormalizeRunnerTags([]string{*t.RunnerTag})
	}
	matchMode := t.RunnerTagMatchMode
	if matchMode != db.RunnerTagMatchAny {
		matchMode = db.RunnerTagMatchAll
	}

	var projectRunners []db.Runner
	projectRunners, err = t.taskPool.store.GetRunners(
		t.Task.ProjectID, false, db.RunnerFilterIgnoreTags, nil,
	)
	if err != nil {
		return
	}

	var globalRunners []db.Runner
	globalRunners, err = t.taskPool.store.GetAllRunners(
		false, true, db.RunnerFilterIgnoreTags, nil,
	)
	if err != nil {
		return
	}
	candidates := make([]RunnerPlacementCandidate, 0, len(projectRunners)+len(globalRunners))
	for _, runner := range append(projectRunners, globalRunners...) {
		candidates = append(candidates, RunnerPlacementCandidate{
			Runner: runner, RunningTasks: t.taskPool.GetNumberOfRunningTasksOfRunner(runner.ID),
		})
	}

	for {
		decision := DecideRunnerPlacement(
			t.Task.ProjectID, requestedTags, matchMode, candidates,
			tz.Now(), util.Config.RunnersOfflineTimeout(), t.ExecutorImage,
		)
		if decision.SelectedRunnerID == nil {
			persisted, persistErr := t.taskPool.store.SetTaskRunnerPlacement(
				tsk.Task.ProjectID, tsk.Task.ID, decision,
			)
			if persistErr != nil {
				return persistErr
			}
			if !persisted {
				return fmt.Errorf("task placement changed concurrently")
			}
			tsk.Task.PlacementDecision = &decision
			tsk.Log("Runner placement delayed: " + decision.Reason + ". " + decision.ActionHint)
			return ErrAllRunnersBusy
		}

		selectedIndex := -1
		for index := range candidates {
			if candidates[index].Runner.ID == *decision.SelectedRunnerID {
				selectedIndex = index
				break
			}
		}
		if selectedIndex < 0 {
			return fmt.Errorf("selected runner disappeared from placement candidates")
		}
		runner := &candidates[selectedIndex].Runner

		assignedTask, assigned, assignErr := t.taskPool.store.AssignTaskRunner(
			tsk.Task.ProjectID, tsk.Task.ID, runner.ID, runner.Name, tz.Now(), decision,
		)
		if assignErr != nil {
			return assignErr
		}
		if !assigned {
			fresh, freshErr := t.taskPool.store.GetTask(tsk.Task.ProjectID, tsk.Task.ID)
			if freshErr != nil {
				return freshErr
			}
			if fresh.RunnerID != nil ||
				(fresh.Status != task_logger.TaskWaitingStatus && fresh.Status != task_logger.TaskStartingStatus) {
				return fmt.Errorf("task assignment changed concurrently")
			}
			// The task is still assignable, so the runner lost a concurrent
			// capacity claim. Mark it full and deterministically try the next one.
			if candidates[selectedIndex].Runner.MaxParallelTasks > 0 {
				candidates[selectedIndex].RunningTasks = candidates[selectedIndex].Runner.MaxParallelTasks
			} else {
				candidates = append(candidates[:selectedIndex], candidates[selectedIndex+1:]...)
			}
			continue
		}
		tsk.Task.RunnerID = assignedTask.RunnerID
		tsk.Task.RunnerSnapshotID = assignedTask.RunnerSnapshotID
		tsk.Task.RunnerName = assignedTask.RunnerName
		tsk.Task.AssignmentGeneration = assignedTask.AssignmentGeneration
		tsk.Task.RunnerAssignedAt = assignedTask.RunnerAssignedAt
		tsk.Task.RecoveryReason = assignedTask.RecoveryReason
		tsk.Task.PlacementDecision = assignedTask.PlacementDecision
		t.taskPool.state.UpdateRuntimeFields(tsk)
		if t.taskPool.taskControlLifecycle != nil {
			if err = t.taskPool.taskControlLifecycle.RegisterTaskControl(tsk.Task); err != nil {
				return err
			}
		}

		// Capacity is reserved before an external webhook can start work. If the
		// webhook fails, TaskRunner.run marks this assigned attempt failed, which
		// releases the slot without ever notifying a runner that lost the claim.
		err = callRunnerWebhook(runner, tsk, "start")
		if err != nil {
			return
		}

		tsk.Logf("Task #%d is assigned to runner #%d (attempt %d): %s",
			tsk.Task.ID, runner.ID, tsk.Task.AssignmentGeneration, decision.Reason)

		// The task now runs on the remote runner. Its completion is reported back
		// via the runner API (PUT /runners) and finalized by
		// TaskPool.FinalizeRemoteTask on whichever node receives the terminal
		// status. Returning here instead of polling means the task survives the
		// death or restart of the node that dispatched it: no node-local goroutine
		// owns its completion.
		t.scheduleTimeout(runner)
		return nil
	}
}

// scheduleTimeout enforces util.Config.MaxTaskDurationSec for a dispatched
// remote task. The timer runs node-locally; if this node dies before it fires,
// the HA orphan cleaner applies the same limit as a backstop. Firing on an
// already-finished task is a no-op.
func (t *RemoteJob) scheduleTimeout(runner *db.Runner) {
	if util.Config.MaxTaskDurationSec <= 0 {
		return
	}
	d := time.Duration(util.Config.MaxTaskDurationSec) * time.Second
	taskID := t.Task.ID
	pool := t.taskPool
	time.AfterFunc(d, func() {
		tsk, err := pool.GetTask(taskID)
		if err != nil || tsk == nil {
			return
		}
		if util.HAEnabled() {
			pool.refreshTaskStatusFromDB(tsk)
		}
		if tsk.Task.Status.IsFinished() {
			return
		}
		tsk.Log("Task timed out")
		tsk.SetStatus(task_logger.TaskFailStatus)
		pool.FinalizeRemoteTask(tsk, runner)
	})
}

func (t *RemoteJob) Kill() {
	t.killed = true
	// Do nothing because you can't kill remote process
}

func (t *RemoteJob) IsKilled() bool {
	return t.killed
}

// Async is true: RemoteJob.Run only dispatches the task to a runner; its
// completion is reported asynchronously via the runner API.
func (t *RemoteJob) Async() bool {
	return true
}
