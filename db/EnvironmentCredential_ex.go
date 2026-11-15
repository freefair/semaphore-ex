package db

import (
	"regexp"

	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

// EnvironmentCredentialTarget selects how a referenced global credential
// reaches the task: as an extra variable or as a process environment variable.
type EnvironmentCredentialTarget string

const (
	EnvironmentCredentialTargetVar EnvironmentCredentialTarget = "var"
	EnvironmentCredentialTargetEnv EnvironmentCredentialTarget = "env"
)

// MaxEnvironmentCredentials bounds the references of one Variable Group. A
// task's combined bindings are bounded again by MaxTaskGlobalCredentialBindings.
const MaxEnvironmentCredentials = 64

// EnvironmentCredential references a global credential granted to the
// project. It is value-free: the value is resolved for each task at dispatch,
// after the grant and the task's actor are authorized again.
type EnvironmentCredential struct {
	Name         string                      `json:"name"`
	Target       EnvironmentCredentialTarget `json:"target"`
	CredentialID int                         `json:"credential_id"`
}

// IsValidEnvironmentCredentialName reports whether name is usable for target.
// Extra variable names follow the existing task binding rule (lowercase);
// environment variable names follow POSIX, so Terraform's TF_VAR_<name>
// form with a lowercase variable name works.
func IsValidEnvironmentCredentialName(target EnvironmentCredentialTarget, name string) bool {
	switch target {
	case EnvironmentCredentialTargetVar:
		return regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(name)
	case EnvironmentCredentialTargetEnv:
		return regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(name) && name != "SEMAPHORE_JWT"
	default:
		return false
	}
}

// ValidateEnvironmentCredentials checks the structure of a Variable Group's
// references. Whether each credential is granted to the project is checked by
// the API, which knows the project and the user.
func ValidateEnvironmentCredentials(credentials []EnvironmentCredential) error {
	if len(credentials) > MaxEnvironmentCredentials {
		return common_errors.NewValidationError("a variable group can reference at most 64 global credentials")
	}
	names := make(map[string]struct{}, len(credentials))
	for _, credential := range credentials {
		if credential.CredentialID <= 0 {
			return common_errors.NewValidationError("global credential reference " + credential.Name + " needs a credential")
		}
		if !IsValidEnvironmentCredentialName(credential.Target, credential.Name) {
			return common_errors.NewValidationError("global credential reference name " + credential.Name + " is invalid: " +
				"extra variables use lowercase letters, digits and _; environment variables use letters, digits and _ and do not start with a digit")
		}
		if _, duplicate := names[credential.Name]; duplicate {
			return common_errors.NewValidationError("global credential reference name " + credential.Name + " is used twice")
		}
		names[credential.Name] = struct{}{}
	}
	return nil
}
