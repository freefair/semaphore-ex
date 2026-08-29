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
		{"terminal reconciles without replacement", TaskExecutionEvidence{State: TaskExecutionTerminal, TerminalStatus: "success"}, TaskRecoveryRecover, false},
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

func TestNewTaskExecutionIdentityIsStableAndGenerationScoped(t *testing.T) {
	first, err := NewTaskExecutionIdentity(41, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewTaskExecutionIdentity(41, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.StableID != "runner:7:task:41:generation:3" {
		t.Fatalf("unexpected stable execution identity: %#v %#v", first, second)
	}
	different, err := NewTaskExecutionIdentity(41, 7, 4)
	if err != nil {
		t.Fatal(err)
	}
	if different.StableID == first.StableID {
		t.Fatal("a replacement generation reused the prior execution identity")
	}
}
