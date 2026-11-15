package db

import (
	"regexp"
	"strings"

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
// after the grant and the task's actor are authorized again. The reference
// deliberately outlives a deleted credential, so the task is blocked instead
// of running without the value.
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
		// Names are compared case-insensitively: MySQL's default collations
		// would otherwise reject TOKEN beside token while SQLite and
		// PostgreSQL accept it.
		folded := strings.ToLower(credential.Name)
		if _, duplicate := names[folded]; duplicate {
			return common_errors.NewValidationError("global credential reference name " + credential.Name + " is used twice")
		}
		names[folded] = struct{}{}
	}
	return nil
}

// CredentialPrincipalKind names the trigger a credential principal belongs to.
type CredentialPrincipalKind string

const (
	CredentialPrincipalSchedule    CredentialPrincipalKind = "schedule"
	CredentialPrincipalIntegration CredentialPrincipalKind = "integration"
)

// TaskCredentialPrincipalRepository records who answers for the global
// credentials a schedule or an integration consumes. Their tasks have no
// starting user; the principal is the user who last created or changed the
// trigger, and the resolver checks that user's permissions at dispatch.
// *SqlDb implements it; callers obtain it by type assertion.
type TaskCredentialPrincipalRepository interface {
	// SetScheduleCredentialPrincipal and SetIntegrationCredentialPrincipal
	// record the principal of a newly created trigger.
	SetScheduleCredentialPrincipal(scheduleID int, userID int) error
	SetIntegrationCredentialPrincipal(integrationID int, userID int) error
	// BeginCredentialPrincipalChange claims a trigger for one change and
	// removes its principal until the change finishes. ok is false while
	// another change holds a fresh claim.
	BeginCredentialPrincipalChange(kind CredentialPrincipalKind, triggerID int) (token string, ok bool, err error)
	// FinishCredentialPrincipalChange makes userID the principal if the
	// claim is still this change's.
	FinishCredentialPrincipalChange(kind CredentialPrincipalKind, triggerID int, token string, userID int) error
	// AbortCredentialPrincipalChange releases the claim and leaves the
	// trigger without principal.
	AbortCredentialPrincipalChange(kind CredentialPrincipalKind, triggerID int, token string) error
	// GetTaskCredentialPrincipal returns the principal of the schedule or
	// integration that started the task; ok is false when there is none or
	// a change is in progress.
	GetTaskCredentialPrincipal(task Task) (userID int, ok bool, err error)
}
