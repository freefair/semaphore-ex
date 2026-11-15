package tasks

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
)

// credentialEnvVars returns NAME=value pairs for Variable Group references to
// global credentials with the environment target. The server injected their
// values into the task-secret payload at dispatch; the references themselves
// are value-free and travel with the task's merged Variable Group.
func (t *LocalExecutor) credentialEnvVars() ([]string, error) {
	names := t.credentialEnvNames()
	if len(names) == 0 {
		return nil, nil
	}
	values := make(map[string]any)
	if t.Secret != "" {
		if err := json.Unmarshal([]byte(t.Secret), &values); err != nil {
			return nil, err
		}
	}
	res := make([]string, 0, len(names))
	for _, name := range names {
		value, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("global credential %s was not resolved for this task", name)
		}
		res = append(res, name+"="+formatVarValue(value))
	}
	return res, nil
}

// removeCredentialEnvTargets keeps environment-target credentials out of the
// extra variables, so each value reaches the task exactly once and never as a
// command-line argument.
func (t *LocalExecutor) removeCredentialEnvTargets(extraVars map[string]any) {
	for _, name := range t.credentialEnvNames() {
		delete(extraVars, name)
	}
}

func (t *LocalExecutor) credentialEnvNames() []string {
	var names []string
	for _, reference := range t.Environment.Credentials {
		if reference.Target == db.EnvironmentCredentialTargetEnv {
			names = append(names, reference.Name)
		}
	}
	return names
}
