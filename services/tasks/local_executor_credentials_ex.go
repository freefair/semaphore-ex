package tasks

import (
	"encoding/json"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
)

// credentialEnvVars returns NAME=value pairs for the Variable Group's global
// credential references that reach the task through its process environment.
// The server injected the values into the task-secret payload at dispatch;
// the references themselves are value-free and travel with the task's merged
// Variable Group.
//
// Global credentials never become command-line arguments, where process
// listings and diagnostics could read them. Environment targets are passed
// as they are named. Extra-variable targets stay extra variables for Ansible,
// which receives secret extra variables in an encrypted file; Terraform and
// OpenTofu receive them as TF_VAR_<name>, every other application under
// their own name in the environment.
func (t *LocalExecutor) credentialEnvVars() ([]string, error) {
	targets := t.credentialEnvTargets()
	if len(targets) == 0 {
		return nil, nil
	}
	values := make(map[string]any)
	if t.Secret != "" {
		if err := json.Unmarshal([]byte(t.Secret), &values); err != nil {
			return nil, err
		}
	}
	res := make([]string, 0, len(targets))
	for _, target := range targets {
		value, ok := values[target.name]
		if !ok {
			return nil, fmt.Errorf("global credential %s was not resolved for this task", target.name)
		}
		res = append(res, target.variable+"="+formatVarValue(value))
	}
	return res, nil
}

// removeCredentialEnvTargets keeps credentials delivered through the
// environment out of the extra variables, so each value reaches the task
// exactly once and never as a command-line argument.
func (t *LocalExecutor) removeCredentialEnvTargets(extraVars map[string]any) {
	for _, target := range t.credentialEnvTargets() {
		delete(extraVars, target.name)
	}
}

type credentialEnvTarget struct {
	// name is the reference name, the key in the task-secret payload.
	name string
	// variable is the process environment variable that carries the value.
	variable string
}

func (t *LocalExecutor) credentialEnvTargets() []credentialEnvTarget {
	var targets []credentialEnvTarget
	for _, reference := range t.Environment.Credentials {
		switch {
		case reference.Target == db.EnvironmentCredentialTargetEnv:
			targets = append(targets, credentialEnvTarget{name: reference.Name, variable: reference.Name})
		case t.Template.App == db.AppAnsible || t.Template.App == "":
			// Ansible keeps extra-variable targets in the encrypted file.
		case t.Template.App.IsTerraform():
			targets = append(targets, credentialEnvTarget{name: reference.Name, variable: "TF_VAR_" + reference.Name})
		default:
			targets = append(targets, credentialEnvTarget{name: reference.Name, variable: reference.Name})
		}
	}
	return targets
}
