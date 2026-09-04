package haresilience

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalWorkflowRequestUsesExplicitOwnerPolicy(t *testing.T) {
	request := approvalWorkflowRequest("approve?")
	nodes, ok := request["nodes"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, nodes, 1)
	policy, ok := nodes[0]["approval_role_policy"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "any_of", policy["mode"])
	assert.Equal(t, []string{"builtin:owner"}, policy["role_ids"])
	assert.Equal(t, 1, policy["minimum_distinct_approvers"])
	assert.Equal(t, false, policy["initiator_separation"])
}

func TestParseRecoveryAssignment(t *testing.T) {
	assignment, err := parseRecoveryAssignment("42|server-b|0123456789abcdef0123456789abcdef\n")
	if err != nil {
		t.Fatal(err)
	}
	if assignment.TaskID != 42 || assignment.OwnerNodeID != "server-b" || assignment.OwnerBootID != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected assignment: %+v", assignment)
	}

	for _, invalid := range []string{"", "42", "zero|server-a|0123456789abcdef0123456789abcdef", "42|server-c|0123456789abcdef0123456789abcdef", "42|server-a|not-a-boot-id"} {
		if _, err := parseRecoveryAssignment(invalid); err == nil {
			t.Fatalf("invalid assignment %q was accepted", invalid)
		}
	}
}
