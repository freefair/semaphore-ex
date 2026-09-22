package db_lib

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

// runTerraformCommand leaves shutdown and state unlock to the CLI. A second
// interrupt or an automatic kill can interrupt state persistence, so cancellation
// keeps the task active until the command actually exits.
func runTerraformCommand(cmd *exec.Cmd, stopCh <-chan struct{}, logger task_logger.Logger) error {
	select {
	case <-stopCh:
		return nil
	default:
	}
	prepareTerraformCommand(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-stopCh:
		logger.Log("Gracefully stopping Terraform; waiting for state persistence and lock release. No automatic force kill will be sent.")
		if err := interruptTerraformCommand(cmd); err != nil && !errors.Is(err, os.ErrProcessDone) {
			logger.Logf("Could not interrupt Terraform: %v; waiting for the process to exit without forcing termination", err)
		}
		return <-exited
	}
}

func (r *terraformReader) closeInput() {
	r.mu.Lock()
	r.EOF = true
	r.mu.Unlock()
}

func (r *terraformReader) getStatus() task_logger.TaskStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *terraformReader) Read(p []byte) (int, error) {
	for {
		select {
		case <-r.stopCh:
			return 0, io.EOF
		case <-r.done:
			return 0, io.EOF
		default:
		}
		r.mu.RLock()
		eof, status := r.EOF, r.status
		r.mu.RUnlock()
		if eof || status.IsFinished() || status == task_logger.TaskStoppingStatus {
			return 0, io.EOF
		}
		if status == task_logger.TaskConfirmed || status == task_logger.TaskRejected {
			answer := "yes\n"
			if status == task_logger.TaskRejected {
				answer = "no\n"
			}
			r.closeInput()
			r.logger.SetStatus(task_logger.TaskRunningStatus)
			return copy(p, answer), nil
		}
		select {
		case <-r.stopCh:
			return 0, io.EOF
		case <-r.done:
			return 0, io.EOF
		case <-time.After(100 * time.Millisecond):
		}
	}
}
