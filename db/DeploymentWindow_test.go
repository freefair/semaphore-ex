package db

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeploymentWindowDraftValidationDoesNotMutateNewRuleIDs(t *testing.T) {
	policy := DeploymentWindowPolicy{
		ProjectID: 1, Revision: 1, Timezone: "Europe/Berlin", Default: DeploymentWindowDefaultAllow,
		Rules: []DeploymentWindowRule{{
			ID: 0, Revision: 1, Name: "new freeze", Active: true,
			Kind: DeploymentWindowFreeze, Scope: DeploymentWindowProjectScope,
			Recurrence: "* * * * *", DurationMinutes: 60,
		}},
	}

	require.NoError(t, policy.ValidateDraft(func(value string) error {
		_, err := time.LoadLocation(value)
		return err
	}))
	require.Zero(t, policy.Rules[0].ID, "validation must preserve the repository's new-rule sentinel")
}

func TestDeploymentWindowRuleRejectsRecurrenceLongerThanStorage(t *testing.T) {
	rule := DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "bounded recurrence", Active: true,
		Kind: DeploymentWindowAllow, Scope: DeploymentWindowProjectScope,
		Recurrence: strings.Repeat("0,", 128) + "0 * * * *", DurationMinutes: 1,
	}

	require.Error(t, rule.Validate())
}
