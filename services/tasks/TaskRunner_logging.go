package tasks

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/tz"

	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
)

func (t *TaskRunner) Log(msg string) {
	t.LogWithTime(tz.Now(), msg)
}

func (t *TaskRunner) Logf(format string, a ...any) {
	t.LogfWithTime(tz.Now(), format, a...)
}

func (t *TaskRunner) LogWithTime(now time.Time, msg string) {
	t.sendToWs(now, msg)

	t.pool.logger <- logRecord{
		task:   t,
		output: msg,
		time:   now,
	}

	for _, l := range t.logListeners {
		l(now, msg)
	}
}

func (t *TaskRunner) sendToWs(now time.Time, msg string) {
	for _, user := range t.users {
		b, err := json.Marshal(&map[string]any{
			"type":       "log",
			"output":     msg,
			"time":       now,
			"task_id":    t.Task.ID,
			"project_id": t.Task.ProjectID,
		})

		util.LogPanic(err)
		sockets.Message(user, b)
	}
}

func (t *TaskRunner) LogfWithTime(now time.Time, format string, a ...any) {
	t.LogWithTime(now, fmt.Sprintf(format, a...))
}

func (t *TaskRunner) LogCmd(cmd *exec.Cmd) func() {
	// io.PipeWriter is not *os.File, so os/exec owns and waits for output copying.
	stderr, stderrWriter := io.Pipe()
	stdout, stdoutWriter := io.Pipe()
	cmd.Stderr = stderrWriter
	cmd.Stdout = stdoutWriter

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		t.logPipe(stderr)
	}()
	go func() {
		defer wg.Done()
		t.logPipe(stdout)
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = stderrWriter.Close()
			_ = stdoutWriter.Close()
			wg.Wait()
		})
	}
}

func (t *TaskRunner) SetCommit(hash, message string) {

	t.Task.CommitHash = &hash
	t.Task.CommitMessage = message

	if err := t.pool.store.UpdateTask(t.Task); err != nil {
		t.panicOnError(err, "Failed to update task commit")
	}
}

func (t *TaskRunner) SetStatus(status task_logger.TaskStatus) {
	t.setStatus(status, nil, nil, 0, 0)
}

func taskStatusTransitionAllowed(current task_logger.TaskStatus, next task_logger.TaskStatus) bool {
	if current == next {
		return true
	}
	switch current {
	case task_logger.TaskConfirmed:
		return next != task_logger.TaskWaitingConfirmation
	case task_logger.TaskRunningStatus:
		return next != task_logger.TaskWaitingStatus
	case task_logger.TaskStoppingStatus, task_logger.TaskRejected:
		return next == task_logger.TaskStoppedStatus || next == task_logger.TaskFailStatus
	case task_logger.TaskSuccessStatus, task_logger.TaskFailStatus, task_logger.TaskStoppedStatus:
		return false
	default:
		return true
	}
}

func runnerAttemptOutcomeForStatus(status task_logger.TaskStatus) db.RunnerAttemptOutcome {
	switch status {
	case task_logger.TaskSuccessStatus:
		return db.RunnerAttemptSucceeded
	case task_logger.TaskFailStatus:
		return db.RunnerAttemptFailed
	case task_logger.TaskStoppedStatus:
		return db.RunnerAttemptStopped
	default:
		return db.RunnerAttemptActive
	}
}

// ApplyRunnerProgress atomically accepts one report for the current assignment.
// A false result means the runner must terminate its stale local job.
func (t *TaskRunner) ApplyRunnerProgress(
	status task_logger.TaskStatus,
	runnerID int,
	generation int,
	commitHash *string,
	commitMessage string,
) bool {
	if generation == 0 {
		if t.Task.AssignmentGeneration != 0 || t.Task.RunnerID == nil || *t.Task.RunnerID != runnerID {
			return false
		}
		return t.setStatus(status, commitHash, &commitMessage, 0, 0)
	}
	return t.setStatus(status, commitHash, &commitMessage, runnerID, generation)
}

func (t *TaskRunner) setStatus(
	status task_logger.TaskStatus,
	commitHash *string,
	commitMessage *string,
	expectedRunnerID int,
	expectedGeneration int,
) bool {
	oldStatus := t.Task.Status
	if !taskStatusTransitionAllowed(oldStatus, status) {
		return false
	}
	candidate := t.Task
	candidate.Status = status
	if status == task_logger.TaskRunningStatus && oldStatus != task_logger.TaskRunningStatus {
		now := tz.Now()
		candidate.Start = &now
	}
	if commitHash != nil {
		candidate.CommitHash = commitHash
		candidate.CommitMessage = *commitMessage
	}

	conditionalRunnerUpdate := candidate.RunnerID != nil && candidate.AssignmentGeneration > 0
	if expectedRunnerID > 0 || expectedGeneration > 0 {
		if candidate.RunnerID == nil || *candidate.RunnerID != expectedRunnerID ||
			candidate.AssignmentGeneration != expectedGeneration {
			return false
		}
		conditionalRunnerUpdate = true
	}
	if conditionalRunnerUpdate {
		runnerID := *candidate.RunnerID
		generation := candidate.AssignmentGeneration
		if expectedRunnerID > 0 {
			runnerID = expectedRunnerID
			generation = expectedGeneration
		}
		updated, err := t.pool.store.UpdateTaskRunner(
			candidate, oldStatus, runnerID, generation,
			runnerAttemptOutcomeForStatus(status), "", tz.Now(),
		)
		if err != nil {
			t.panicOnError(err, "Failed to conditionally update runner task status")
			return false
		}
		if !updated {
			t.pool.refreshTaskStatusFromDB(t)
			return false
		}
		t.Task = candidate
		t.pool.state.UpdateRuntimeFields(t)
		if status != oldStatus {
			t.publishStatus()
			t.afterStatusChange(oldStatus, status)
		}
		return true
	}

	if status == oldStatus && commitHash == nil {
		return true
	}
	t.Task = candidate
	t.saveStatus()
	if status != oldStatus {
		t.afterStatusChange(oldStatus, status)
	}
	return true
}

func (t *TaskRunner) afterStatusChange(oldStatus task_logger.TaskStatus, status task_logger.TaskStatus) {
	if t.pool != nil {
		t.pool.metrics.RecordTaskStatusChange(oldStatus, status)
	}

	if localJob, ok := t.job.(*LocalExecutor); ok {
		localJob.SetStatus(status)
	}

	if status == task_logger.TaskFailStatus {
		t.sendMailAlert()
	}

	if status.IsNotifiable() {
		t.sendTelegramAlert()
		t.sendSlackAlert()
		t.sendRocketChatAlert()
		t.sendMicrosoftTeamsAlert()
		t.sendDingTalkAlert()
		t.sendGotifyAlert()
	}

	for _, l := range t.statusListeners {
		l(status)
	}

	log.WithFields(log.Fields{
		"task_id": t.Task.ID,
		"context": "task_logger",
		"status":  status,
	}).Info("Task status updated")
}

func (t *TaskRunner) panicOnError(err error, msg string) {
	if err == nil {
		return
	}

	t.Log(msg)
	util.LogPanicF(err, log.Fields{"error": msg})
}

func (t *TaskRunner) logPipe(reader io.Reader) {
	linesCh := make(chan string, 100000)
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		for line := range linesCh {
			t.Log(line)
		}
	}()
	defer wg.Wait()

	scanner := bufio.NewScanner(reader)
	const maxCapacity = 10 * 1024 * 1024 // 10 MB
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	for scanner.Scan() {
		line := scanner.Text()
		linesCh <- line
	}

	close(linesCh)

	err := scanner.Err()

	if err != nil {
		msg := "Failed to read TaskRunner output"

		switch err.Error() {
		case "EOF",
			"os: process already finished",
			"read |0: file already closed":
			return // it is ok
		case "bufio.Scanner: token too long":
			msg = "TaskRunner output exceeds the maximum allowed size of 10MB"
		}

		t.kill() // kill the job because stdout cannot be read.

		log.WithError(err).WithFields(log.Fields{
			"task_id": t.Task.ID,
			"context": "task_logger",
		}).Error(msg)

		t.Log("Fatal error: " + msg)
	}
}
