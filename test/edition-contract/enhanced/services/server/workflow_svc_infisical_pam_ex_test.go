package server

import (
	"testing"

	"github.com/semaphoreui/semaphore/pro_interfaces"
	"github.com/stretchr/testify/assert"
)

func TestRedactCrossProjectInfisicalPAMHidesSourceRouting(t *testing.T) {
	plan := pro_interfaces.ExecutionPreflightPlan{
		References: []pro_interfaces.ExecutionPreflightReference{
			{Kind: pro_interfaces.ExecutionReferenceSecretStorage, ID: 4, BindingTarget: "ssh: web-1 → servers/web-1", Visible: true},
			{Kind: pro_interfaces.ExecutionReferenceCredential, ID: 7, BindingTarget: "inventory.ssh", Visible: true},
		},
		Findings: []pro_interfaces.ExecutionPreflightFinding{
			{Severity: pro_interfaces.ExecutionFindingDenial, Code: pro_interfaces.ExecutionReasonInfisicalPAMInvalid,
				Message: `Infisical PAM routes cannot be applied: host "web-1" is routed by more than one Infisical storage`},
			{Severity: pro_interfaces.ExecutionFindingWarning, Code: pro_interfaces.ExecutionReasonProvisionalPlacement, Message: "kept"},
		},
	}

	redactCrossProjectInfisicalPAM(&plan)

	assert.Empty(t, plan.References[0].BindingTarget)
	assert.Equal(t, "inventory.ssh", plan.References[1].BindingTarget)
	assert.Equal(t, "Infisical PAM routes of the shared template's project cannot be applied.", plan.Findings[0].Message)
	assert.NotContains(t, plan.Findings[0].Message, "web-1")
	assert.Equal(t, "kept", plan.Findings[1].Message)
}
