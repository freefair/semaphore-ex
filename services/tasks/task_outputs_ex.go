package tasks

import "errors"

// ValidateRemoteTaskOutputs validates an untrusted runner document without
// writing it. The caller must publish the returned canonical value only through
// its runner/generation conditional terminal transition.
func (p *TaskPool) ValidateRemoteTaskOutputs(tsk *TaskRunner, document string) (*string, error) {
	if tsk == nil {
		return nil, errors.New("task is required")
	}
	if tsk.Task.WorkflowRunID == nil {
		return nil, nil
	}
	if p == nil || p.outputsCollector == nil {
		return nil, errors.New("workflow task outputs are not supported by this server")
	}
	return p.outputsCollector.Validate(document)
}
