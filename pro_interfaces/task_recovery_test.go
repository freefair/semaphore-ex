package pro_interfaces

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecideTaskRecoveryRequiresExecutionEvidenceBeforeReplacement(t *testing.T) {
	lease := TaskControlLease{TaskID: 1, OwnerBootID: "boot-a", FencingToken: 3,
		Execution: TaskExecutionIdentity{RunnerID: 2, Generation: 4, StableID: "job-1-4"}}

	for _, test := range []struct {
		name        string
		evidence    TaskExecutionEvidence
		decision    TaskRecoveryDecision
		replacement bool
	}{
		{"running observes", TaskExecutionEvidence{State: TaskExecutionRunning}, TaskRecoveryObserve, false},
		{"absent recovers", TaskExecutionEvidence{State: TaskExecutionAbsent}, TaskRecoveryRecover, true},
		{"terminal recovers", TaskExecutionEvidence{State: TaskExecutionTerminal}, TaskRecoveryRecover, true},
		{"unknown quarantines", TaskExecutionEvidence{State: TaskExecutionUnknown}, TaskRecoveryQuarantine, false},
		{"invalid quarantines", TaskExecutionEvidence{State: "invalid"}, TaskRecoveryQuarantine, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			assessment := DecideTaskRecovery(lease, test.evidence)
			assert.Equal(t, test.decision, assessment.Decision)
			assert.Equal(t, test.replacement, assessment.SafeReplacement)
		})
	}
}
