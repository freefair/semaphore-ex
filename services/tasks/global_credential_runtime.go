package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const globalCredentialBlockedMessage = "Task blocked: credential access is no longer permitted. Review the credential grant and task permissions."

// ResolveTaskGlobalCredentials merges resolved task-scoped values into the
// existing encrypted task-secret payload. The persisted task contains only
// value-free bindings; values exist only in this dispatch-local string.
//
// Bindings come from the task itself (workflow secret parameters) and from
// the global credential references of the template's Variable Groups.
func (p *TaskPool) ResolveTaskGlobalCredentials(
	ctx context.Context,
	runner *TaskRunner,
	runnerID *int,
	taskSecret string,
) (string, error) {
	bindings, err := runner.taskGlobalCredentialBindings()
	if err != nil {
		// A configuration conflict, not an authorization decision: name it
		// in the task log so it can be fixed. The message carries no value.
		runner.Log("Global credentials: " + err.Error())
		return "", err
	}
	if len(bindings) == 0 {
		return taskSecret, nil
	}
	if p.globalCredentialResolver == nil {
		return "", errors.New("global credential resolver unavailable")
	}
	injector, err := newTaskGlobalCredentialInjector(taskSecret)
	if err != nil {
		return "", err
	}
	actorID := 0
	if runner.Task.UserID != nil {
		actorID = *runner.Task.UserID
	}
	principal := &pro_interfaces.GlobalCredentialPrincipalMemo{}
	for _, binding := range bindings {
		_, resolveErr := p.globalCredentialResolver.ResolveAndInject(ctx, pro_interfaces.GlobalCredentialResolutionRequest{
			TaskID: runner.Task.ID, ProjectID: runner.Task.ProjectID, TemplateID: runner.Task.TemplateID,
			ActorID: actorID, RunnerID: runnerID, DispatchGeneration: runner.Task.AssignmentGeneration,
			Binding: binding, Principal: principal,
		}, injector)
		if resolveErr != nil {
			return "", resolveErr
		}
	}
	return injector.encode()
}

// taskGlobalCredentialBindings returns every binding of the task in a stable
// order. A Variable Group reference never replaces a workflow binding of the
// same name silently: the task is blocked instead.
func (t *TaskRunner) taskGlobalCredentialBindings() ([]pro_interfaces.GlobalCredentialTaskBinding, error) {
	if t.Task.GlobalCredentialBindings == nil && t.Task.GlobalCredentialBindingsJSON != "" {
		if err := t.Task.DecodeGlobalCredentialBindings(); err != nil {
			return nil, errors.New("task global credential bindings are invalid")
		}
	}
	bindings := make([]pro_interfaces.GlobalCredentialTaskBinding, 0, len(t.Task.GlobalCredentialBindings)+len(t.Environment.Credentials))
	for name, credentialID := range t.Task.GlobalCredentialBindings {
		bindings = append(bindings, pro_interfaces.GlobalCredentialTaskBinding{CredentialID: credentialID, Target: name})
	}
	groupSecrets := make(map[string]struct{}, len(t.Environment.Secrets))
	for _, secret := range t.Environment.Secrets {
		groupSecrets[secret.Name] = struct{}{}
	}
	for _, reference := range t.Environment.Credentials {
		if _, exists := t.Task.GlobalCredentialBindings[reference.Name]; exists {
			return nil, errors.New("variable group credential " + reference.Name + " conflicts with a workflow binding")
		}
		if _, exists := groupSecrets[reference.Name]; exists {
			return nil, errors.New("variable group credential " + reference.Name + " conflicts with a variable group secret")
		}
		bindings = append(bindings, pro_interfaces.GlobalCredentialTaskBinding{
			CredentialID: reference.CredentialID, Target: reference.Name,
			Env: reference.Target == db.EnvironmentCredentialTargetEnv,
		})
	}
	if len(bindings) > db.MaxTaskGlobalCredentialBindings {
		return nil, errors.New("task global credential bindings exceed maximum")
	}
	sort.Slice(bindings, func(left, right int) bool { return bindings[left].Target < bindings[right].Target })
	return bindings, nil
}

// GlobalCredentialBindingTargets names every value injected from a global
// credential, so logs redact them on the server and on runners.
func (t *TaskRunner) GlobalCredentialBindingTargets() []string {
	bindings, err := t.taskGlobalCredentialBindings()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		names = append(names, binding.Target)
	}
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
