package tasks

import "context"

// ConfirmStop preserves the runner's tracking until protected local execution
// has actually returned and cleaned up. A cancellation request alone cannot
// release a task group's slot or establish that Terraform released its lock.
// Pending confirmation is nonblocking so the runner can continue its heartbeat
// and report progress while Terraform finishes a slow state write or unlock.
func (t *LocalExecutor) ConfirmStop(_ context.Context) StopConfirmation {
	t.Kill()
	if !t.Template.App.IsTerraform() && len(t.Task.TaskGroupKeys) == 0 && len(t.Template.TaskGroups) == 0 {
		return StopConfirmed
	}
	t.mu.Lock()
	done := t.runDone
	t.mu.Unlock()
	if done == nil {
		// Kill and Run synchronize on the same mutex. A run that has not begun
		// observes terminationRequested before it can prepare or launch an app.
		return StopConfirmed
	}
	select {
	case <-done:
		return StopConfirmed
	default:
		return StopPending
	}
}
