package db

import (
	"strings"
	"testing"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkflowConditionCompileAndEvaluateTypedExpressions(t *testing.T) {
	result := coreDB.WorkflowNodeResult{
		Status: coreDB.WorkflowRunNodeSucceeded, Successful: true,
		Summary: &coreDB.WorkflowNodeResultSummary{
			State: coreDB.TaskSummaryComplete, ExpectedHosts: 4, TotalHosts: 4, OkHosts: 3, FailedHosts: 1,
		},
	}
	tests := []struct {
		expression string
		matched    bool
	}{
		{`result.status == "succeeded"`, true},
		{`result.successful && result.summary.available`, true},
		{`result.summary.failed_hosts > 0 && result.summary.ok_hosts >= 3`, true},
		{`result.summary.state == "complete" && !(result.summary.total_hosts < 4)`, true},
		{`false || true && false`, false},
		{`(false || true) && !false`, true},
		{`result.status == "failed" || result.summary.failed_hosts == 0`, false},
	}

	for _, test := range tests {
		t.Run(test.expression, func(t *testing.T) {
			program, err := CompileWorkflowCondition(test.expression)
			require.NoError(t, err)
			first, err := EvaluateWorkflowCondition(program, result)
			require.NoError(t, err)
			second, err := EvaluateWorkflowCondition(program, result)
			require.NoError(t, err)
			assert.Equal(t, test.matched, first)
			assert.Equal(t, first, second, "immutable inputs must evaluate deterministically")
		})
	}
}

func TestWorkflowConditionMissingSummaryUsesOnlySafeDefaults(t *testing.T) {
	program, err := CompileWorkflowCondition(
		`!result.summary.available && result.summary.failed_hosts == 0 && result.summary.state == ""`,
	)
	require.NoError(t, err)

	matched, err := EvaluateWorkflowCondition(program, coreDB.WorkflowNodeResult{Status: coreDB.WorkflowRunNodeSucceeded})
	require.NoError(t, err)
	assert.True(t, matched)
}

func TestWorkflowConditionRejectsUnsafeMalformedAndMismatchedInput(t *testing.T) {
	expressions := []string{
		`result.secret == "value"`,
		`result.output.password == "value"`,
		`env.HOME == "/tmp"`,
		`exec("id")`,
		`result.successful > false`,
		`result.summary.failed_hosts == "1"`,
		`result.status`,
		`result.status = "succeeded"`,
		`(result.successful`,
		strings.Repeat("x", maxWorkflowConditionLength+1),
	}

	for _, expression := range expressions {
		t.Run(expression[:min(len(expression), 40)], func(t *testing.T) {
			_, err := CompileWorkflowCondition(expression)
			require.Error(t, err)
		})
	}
}

func TestWorkflowConditionEvaluatorRejectsUntrustedPrograms(t *testing.T) {
	result := coreDB.WorkflowNodeResult{Status: coreDB.WorkflowRunNodeSucceeded, Successful: true}
	programs := []coreDB.WorkflowConditionProgram{
		{Version: 99, Instructions: []coreDB.WorkflowConditionInstruction{{Operation: "literal"}}},
		{Version: 1, Instructions: []coreDB.WorkflowConditionInstruction{{Operation: "shell"}}},
		{Version: 1, Instructions: []coreDB.WorkflowConditionInstruction{{Operation: "field", Field: "result.secret"}}},
		{Version: 1, Instructions: []coreDB.WorkflowConditionInstruction{{Operation: "and"}}},
	}
	for _, program := range programs {
		_, err := EvaluateWorkflowCondition(program, result)
		require.Error(t, err)
	}
}

func TestPrepareWorkflowTemplateStoresCompiledConditionsAndDefaults(t *testing.T) {
	workflow := validWorkflow()
	workflow.MaxParallelTasks = 0
	workflow.Nodes[1].JoinMode = ""
	workflow.Edges[0].Condition = coreDB.WorkflowEdgeExpression
	workflow.Edges[0].Expression = `result.summary.failed_hosts == 0`

	prepared, validation, err := PrepareWorkflowTemplate(validWorkflowStore(), workflow)
	require.NoError(t, err)
	require.True(t, validation.Valid)
	assert.Equal(t, DefaultWorkflowParallelism, prepared.MaxParallelTasks)
	assert.Equal(t, coreDB.WorkflowJoinAllSuccessful, prepared.Nodes[1].JoinMode)
	assert.Equal(t, 1, prepared.Edges[0].ConditionProgram.Version)
	assert.NotEmpty(t, prepared.Edges[0].ConditionProgram.Instructions)
	assert.NotEmpty(t, prepared.Edges[0].ConditionProgramJSON)
}

func TestPrepareWorkflowTemplateOnFailureMatchesStoppedPredecessor(t *testing.T) {
	workflow := validWorkflow()
	workflow.Edges[0].Condition = coreDB.WorkflowEdgeOnFailure

	prepared, validation, err := PrepareWorkflowTemplate(validWorkflowStore(), workflow)
	require.NoError(t, err)
	require.True(t, validation.Valid)

	matched, err := EvaluateWorkflowCondition(
		prepared.Edges[0].ConditionProgram,
		coreDB.WorkflowNodeResult{Status: coreDB.WorkflowRunNodeStopped},
	)
	require.NoError(t, err)
	assert.True(t, matched)
}

func TestValidateWorkflowTemplateReportsExpressionJoinAndParallelismPaths(t *testing.T) {
	workflow := validWorkflow()
	workflow.MaxParallelTasks = MaxWorkflowParallelism + 1
	workflow.Nodes[1].JoinMode = "sometimes"
	workflow.Edges[0].Condition = coreDB.WorkflowEdgeExpression
	workflow.Edges[0].Expression = `result.secret == "value"`

	result, err := ValidateWorkflowTemplate(validWorkflowStore(), workflow)
	require.NoError(t, err)
	require.False(t, result.Valid)
	assert.Contains(t, issueCodes(result), "WORKFLOW_PARALLELISM_INVALID")
	assert.Contains(t, issueCodes(result), "WORKFLOW_JOIN_MODE_INVALID")
	assert.Contains(t, issueCodes(result), "WORKFLOW_EDGE_EXPRESSION_INVALID")
	assert.Contains(t, issuePaths(result), "max_parallel_tasks")
	assert.Contains(t, issuePaths(result), "nodes[1].join_mode")
	assert.Contains(t, issuePaths(result), "edges[0].condition_expression")
}

func issuePaths(result coreDB.WorkflowValidationResult) []string {
	paths := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		paths = append(paths, issue.Path)
	}
	return paths
}
