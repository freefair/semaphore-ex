package db

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploymentWindowRuleRejectsRecurrenceLongerThanStorage(t *testing.T) {
	rule := DeploymentWindowRule{
		ID: 1, Revision: 1, Name: "bounded recurrence", Active: true,
		Kind: DeploymentWindowAllow, Scope: DeploymentWindowProjectScope,
		Recurrence: strings.Repeat("0,", 128) + "0 * * * *", DurationMinutes: 1,
	}

	require.Error(t, rule.Validate())
}
