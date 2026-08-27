package tasks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/semaphoreui/semaphore/api/sockets"
	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pkg/tz"
	"github.com/semaphoreui/semaphore/pro/pkg/stage_parsers"
	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/semaphoreui/semaphore/util"
	log "github.com/sirupsen/logrus"
	"io"
	"os/exec"
	"sync"
	"time"
)

func (t *TaskRunner) Log(msg string) {
	t.LogWithTime(tz.Now(), msg)
}

func (t *TaskRunner) Logf(format string, a ...any) {
	t.LogfWithTime(tz.Now(), format, a...)
}

func (t *TaskRunner) LogWithTime(now time.Time, msg string) {
	if t.Template.App == db.AppAnsible {
		event, recognized, err := stage_parsers.ParseTaskSummaryEvent(msg)
		if recognized {
			if err == nil {
				if t.pool.ansibleTaskRepo == nil {
					err = errors.New("task summary repository is not configured")
				} else {
					err = t.pool.ansibleTaskRepo.IngestTaskSummaryEvent(
						t.Task.ProjectID, t.Task.ID, event, now,
					)
				}
				if writeErr := t.pool.logWriteService.WriteResult(pro_interfaces.ResultLogRecord{
					TaskID:        t.Task.ID,
					ProjectID:     t.Task.ProjectID,
					CorrelationID: event.EventID,
					EventType:     string(event.Kind),
					Result:        event,
				}); writeErr != nil {
					log.WithError(writeErr).WithFields(log.Fields{
						"context": "structured_result_log", "project_id": t.Task.ProjectID, "task_id": t.Task.ID,
					}).Warn("failed to enqueue structured task result")
				}
			}
			if err != nil {
				log.WithError(err).WithFields(log.Fields{
					"context": "task_summary", "project_id": t.Task.ProjectID, "task_id": t.Task.ID,
				}).Warn("failed to persist task summary result")
			}
			return
		}
	}
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
