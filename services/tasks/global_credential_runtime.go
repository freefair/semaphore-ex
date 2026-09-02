package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const globalCredentialBlockedMessage = "Task blocked: credential access is no longer permitted. Review the credential grant and task permissions."

// resolveTaskGlobalCredentials merges resolved task-scoped values into the
// existing encrypted task-secret payload. The persisted task contains only
// value-free bindings; values exist only in this dispatch-local string.
func (p *TaskPool) ResolveTaskGlobalCredentials(
	ctx context.Context,
	runner *TaskRunner,
	runnerID *int,
	taskSecret string,
) (string, error) {
	if runner.Task.GlobalCredentialBindings == nil && runner.Task.GlobalCredentialBindingsJSON != "" {
		if err := runner.Task.DecodeGlobalCredentialBindings(); err != nil {
			return "", errors.New("task global credential bindings are invalid")
		}
	}
	if len(runner.Task.GlobalCredentialBindings) == 0 {
		return taskSecret, nil
	}
	if p.globalCredentialResolver == nil {
		return "", errors.New("global credential resolver unavailable")
	}
	injector, err := newTaskGlobalCredentialInjector(taskSecret)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(runner.Task.GlobalCredentialBindings))
	for name := range runner.Task.GlobalCredentialBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	actorID := 0
	if runner.Task.UserID != nil {
		actorID = *runner.Task.UserID
	}
	for _, name := range names {
		credentialID := runner.Task.GlobalCredentialBindings[name]
		_, resolveErr := p.globalCredentialResolver.ResolveAndInject(ctx, pro_interfaces.GlobalCredentialResolutionRequest{
			TaskID: runner.Task.ID, ProjectID: runner.Task.ProjectID, TemplateID: runner.Task.TemplateID,
			ActorID: actorID, RunnerID: runnerID, DispatchGeneration: runner.Task.AssignmentGeneration,
			Binding: pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credentialID, Target: name},
		}, injector)
		if resolveErr != nil {
			return "", resolveErr
		}
	}
	return injector.encode()
}

func (t *TaskRunner) GlobalCredentialBindingTargets() []string {
	names := make([]string, 0, len(t.Task.GlobalCredentialBindings))
	for name := range t.Task.GlobalCredentialBindings {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type taskGlobalCredentialInjector struct {
	values map[string]any
}

func newTaskGlobalCredentialInjector(taskSecret string) (*taskGlobalCredentialInjector, error) {
	values := make(map[string]any)
	if taskSecret != "" && json.Unmarshal([]byte(taskSecret), &values) != nil {
		return nil, errors.New("task secret payload is invalid")
	}
	return &taskGlobalCredentialInjector{values: values}, nil
}

func (i *taskGlobalCredentialInjector) InjectGlobalCredential(_ context.Context, target string, material []byte) error {
	if !utf8.Valid(material) {
		return errors.New("credential material is not valid text")
	}
	if _, exists := i.values[target]; exists {
		return errors.New("task credential target conflicts with an existing secret")
	}
	i.values[target] = string(material)
	return nil
}

func (i *taskGlobalCredentialInjector) encode() (string, error) {
	encoded, err := json.Marshal(i.values)
	if err != nil {
		return "", errors.New("task credential injection failed")
	}
	return string(encoded), nil
}

func (t *TaskRunner) BlockGlobalCredentialResolution() {
	t.Task.Message = globalCredentialBlockedMessage
	t.SetStatus(task_logger.TaskBlockedStatus)
}
