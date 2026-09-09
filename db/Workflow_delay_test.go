package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkflowDelayDefinitionValidationRejectsMissingAndUnboundedDuration(t *testing.T) {
	tests := []struct {
		name  string
		node  WorkflowNode
		valid bool
	}{
		{name: "positive delay", node: WorkflowNode{Kind: WorkflowNodeDelayKind, DelaySeconds: workflowDelayIntPointer(1)}, valid: true},
		{name: "missing duration", node: WorkflowNode{Kind: WorkflowNodeDelayKind}, valid: false},
		{name: "zero duration", node: WorkflowNode{Kind: WorkflowNodeDelayKind, DelaySeconds: workflowDelayIntPointer(0)}, valid: false},
		{name: "negative duration", node: WorkflowNode{Kind: WorkflowNodeDelayKind, DelaySeconds: workflowDelayIntPointer(-1)}, valid: false},
		{name: "unbounded duration", node: WorkflowNode{Kind: WorkflowNodeDelayKind, DelaySeconds: workflowDelayIntPointer(MaxWorkflowDelaySeconds + 1)}, valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.node.ValidateDelay()
			assert.Equal(t, tt.valid, err == nil)
		})
	}
}

func workflowDelayIntPointer(value int) *int { return &value }
