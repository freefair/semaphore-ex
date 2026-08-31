// Package db implements the clean-room enhanced workflow domain contract.
package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	coreDB "github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
)

const (
	MaxWorkflowNodes           = 200
	MaxWorkflowEdges           = 1000
	DefaultWorkflowParallelism = 4
	MaxWorkflowParallelism     = 32
	maxWorkflowNameLength      = 255
	maxWorkflowLabelLength     = 255
)

func NormalizeWorkflowTemplate(workflow coreDB.WorkflowTemplate) coreDB.WorkflowTemplate {
	workflow.Name = strings.TrimSpace(workflow.Name)
	for index := range workflow.ParameterDefinitions {
		parameter := &workflow.ParameterDefinitions[index]
		parameter.Name = strings.TrimSpace(parameter.Name)
		parameter.Description = strings.TrimSpace(parameter.Description)
		for optionIndex := range parameter.SecretOptions {
			parameter.SecretOptions[optionIndex].Label = strings.TrimSpace(parameter.SecretOptions[optionIndex].Label)
		}
	}
	if workflow.DefinitionVersion == 0 {
		workflow.DefinitionVersion = coreDB.WorkflowDefinitionVersion
	}
	if workflow.MaxParallelTasks == 0 {
		workflow.MaxParallelTasks = DefaultWorkflowParallelism
	}
	nextEdgeID := -1
	usedEdgeIDs := make(map[int]struct{}, len(workflow.Edges))
	for index := range workflow.Edges {
		edge := &workflow.Edges[index]
		if edge.Condition == "" {
			edge.Condition = coreDB.WorkflowEdgeOnSuccess
		}
		edge.Expression = strings.TrimSpace(edge.Expression)
		if edge.Expression != "" {
			edge.Condition = coreDB.WorkflowEdgeExpression
		}
		edge.Label = strings.TrimSpace(edge.Label)
		if edge.ID == 0 {
			for {
				if _, exists := usedEdgeIDs[nextEdgeID]; !exists {
					edge.ID = nextEdgeID
					nextEdgeID--
					break
				}
				nextEdgeID--
			}
		}
		usedEdgeIDs[edge.ID] = struct{}{}
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.Kind == "" {
			node.Kind = coreDB.WorkflowNodeTaskKind
		}
		if node.ConvergenceMode == "" {
			node.ConvergenceMode = coreDB.WorkflowConvergenceAll
		}
		if node.JoinMode == "" {
			node.JoinMode = node.EffectiveJoinMode()
		}
		if node.Kind == coreDB.WorkflowNodeApprovalKind {
			node.ApprovalMessage = trimWorkflowApprovalMessage(node.ApprovalMessage)
			if node.ApprovalPermission == 0 {
				node.ApprovalPermission = coreDB.CanRunProjectTasks
			}
			if node.ApprovalTimeoutOutcome == "" {
				node.ApprovalTimeoutOutcome = coreDB.WorkflowApprovalTimeoutReject
			}
		}
		for outputIndex := range node.ArtifactOutputs {
			output := &node.ArtifactOutputs[outputIndex]
			output.Name = strings.TrimSpace(output.Name)
			if output.MaxBytes == 0 {
				output.MaxBytes = coreDB.DefaultWorkflowArtifactMaxBytes
			}
		}
		for inputIndex := range node.ArtifactInputs {
			input := &node.ArtifactInputs[inputIndex]
			input.Name = strings.TrimSpace(input.Name)
			input.Output = strings.TrimSpace(input.Output)
		}
		node.DisplayName = strings.TrimSpace(node.DisplayName)
	}
	return workflow
}

func PrepareWorkflowTemplate(store coreDB.WorkflowTemplateValidationStore, workflow coreDB.WorkflowTemplate) (coreDB.WorkflowTemplate, coreDB.WorkflowValidationResult, error) {
	workflow = NormalizeWorkflowTemplate(workflow)
	conditionIssues := compileWorkflowConditions(&workflow)
	artifactIssues := prepareWorkflowArtifactMetadata(&workflow)
	parameterIssues := prepareWorkflowParameterMetadata(&workflow)
	policyIssues := prepareWorkflowPolicyMetadata(&workflow)
	result, err := validateWorkflowTemplate(store, workflow)
	if err != nil {
		return coreDB.WorkflowTemplate{}, coreDB.WorkflowValidationResult{}, err
	}
	result.Issues = append(result.Issues, conditionIssues...)
	result.Issues = append(result.Issues, artifactIssues...)
	result.Issues = append(result.Issues, parameterIssues...)
	result.Issues = append(result.Issues, policyIssues...)
	result.Valid = len(result.Issues) == 0
	return workflow, result, nil
}

func prepareWorkflowPolicyMetadata(workflow *coreDB.WorkflowTemplate) []coreDB.WorkflowValidationIssue {
	issues := make([]coreDB.WorkflowValidationIssue, 0)
	if err := workflow.AccessPolicy.Validate(); err != nil {
		issues = append(issues, coreDB.WorkflowValidationIssue{Code: "WORKFLOW_ACCESS_POLICY_INVALID", Message: err.Error(), Path: "access_policy"})
	} else if encoded, err := json.Marshal(workflow.AccessPolicy); err != nil {
		issues = append(issues, coreDB.WorkflowValidationIssue{Code: "WORKFLOW_ACCESS_POLICY_INVALID", Message: "Workflow access policy could not be stored.", Path: "access_policy"})
	} else {
		workflow.AccessPolicyJSON = string(encoded)
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if node.EffectiveKind() != coreDB.WorkflowNodeApprovalKind {
			continue
		}
		if node.ApprovalRolePolicy.Mode == "" && len(node.ApprovalRolePolicy.RoleIDs) == 0 {
			continue // Legacy permission-based approval remains readable until migration.
		}
		if err := node.ApprovalRolePolicy.Validate(); err != nil {
			id := node.ID
			issues = append(issues, coreDB.WorkflowValidationIssue{Code: "WORKFLOW_APPROVAL_ROLE_POLICY_INVALID", Message: err.Error(), Path: fmt.Sprintf("nodes[%d].approval_role_policy", index), NodeID: &id})
		} else if encoded, err := json.Marshal(node.ApprovalRolePolicy); err != nil {
			id := node.ID
			issues = append(issues, coreDB.WorkflowValidationIssue{Code: "WORKFLOW_APPROVAL_ROLE_POLICY_INVALID", Message: "Workflow approval role policy could not be stored.", Path: fmt.Sprintf("nodes[%d].approval_role_policy", index), NodeID: &id})
		} else {
			node.ApprovalRolePolicyJSON = string(encoded)
		}
	}
	return issues
}

func prepareWorkflowParameterMetadata(workflow *coreDB.WorkflowTemplate) []coreDB.WorkflowValidationIssue {
	issues := make([]coreDB.WorkflowValidationIssue, 0)
	if err := coreDB.ValidateWorkflowParameterDeclarations(workflow.ParameterDefinitions); err != nil {
		issues = append(issues, coreDB.WorkflowValidationIssue{
			Code: "WORKFLOW_PARAMETERS_INVALID", Message: err.Error(), Path: "parameters",
		})
	}
	encoded, err := json.Marshal(workflow.ParameterDefinitions)
	if err != nil {
		issues = append(issues, coreDB.WorkflowValidationIssue{
			Code: "WORKFLOW_PARAMETERS_INVALID", Message: "Workflow parameters could not be stored.", Path: "parameters",
		})
	} else {
		workflow.ParameterDefinitionsJSON = string(encoded)
	}
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		if err := coreDB.ValidateWorkflowNodeOverridePolicy(node.OverridePolicy); err != nil {
			id := node.ID
			issues = append(issues, coreDB.WorkflowValidationIssue{
				Code: "WORKFLOW_NODE_OVERRIDE_POLICY_INVALID", Message: err.Error(),
				Path: fmt.Sprintf("nodes[%d].override_policy", index), NodeID: &id,
			})
		}
		encoded, err := json.Marshal(node.OverridePolicy)
		if err != nil {
			id := node.ID
			issues = append(issues, coreDB.WorkflowValidationIssue{
				Code: "WORKFLOW_NODE_OVERRIDE_POLICY_INVALID", Message: "Workflow node override policy could not be stored.",
				Path: fmt.Sprintf("nodes[%d].override_policy", index), NodeID: &id,
			})
		} else {
			node.OverridePolicyJSON = string(encoded)
		}
	}
	return issues
}

func prepareWorkflowArtifactMetadata(workflow *coreDB.WorkflowTemplate) []coreDB.WorkflowValidationIssue {
	issues := coreDB.ValidateWorkflowArtifactGraph(*workflow)
	for index := range workflow.Nodes {
		node := &workflow.Nodes[index]
		outputs, err := json.Marshal(node.ArtifactOutputs)
		if err != nil {
			id := node.ID
			issues = append(issues, coreDB.WorkflowValidationIssue{
				Code: "WORKFLOW_ARTIFACT_OUTPUT_INVALID", Message: "Workflow artifact outputs could not be stored.",
				Path: fmt.Sprintf("nodes[%d].artifact_outputs", index), NodeID: &id,
			})
		} else {
			node.ArtifactOutputsJSON = string(outputs)
		}
		inputs, err := json.Marshal(node.ArtifactInputs)
		if err != nil {
			id := node.ID
			issues = append(issues, coreDB.WorkflowValidationIssue{
				Code: "WORKFLOW_ARTIFACT_INPUT_INVALID", Message: "Workflow artifact inputs could not be stored.",
				Path: fmt.Sprintf("nodes[%d].artifact_inputs", index), NodeID: &id,
			})
		} else {
			node.ArtifactInputsJSON = string(inputs)
		}
	}
	return issues
}

func ValidateWorkflowTemplate(store coreDB.WorkflowTemplateValidationStore, workflow coreDB.WorkflowTemplate) (coreDB.WorkflowValidationResult, error) {
	_, result, err := PrepareWorkflowTemplate(store, workflow)
	return result, err
}

func validateWorkflowTemplate(store coreDB.WorkflowTemplateValidationStore, workflow coreDB.WorkflowTemplate) (coreDB.WorkflowValidationResult, error) {
	issues := make([]coreDB.WorkflowValidationIssue, 0)
	add := func(code, message, path string, nodeID, edgeID *int) {
		issues = append(issues, coreDB.WorkflowValidationIssue{
			Code: code, Message: message, Path: path, NodeID: nodeID, EdgeID: edgeID,
		})
	}
	if workflow.Name == "" {
		add("WORKFLOW_NAME_REQUIRED", "Workflow name is required.", "name", nil, nil)
	} else if len(workflow.Name) > maxWorkflowNameLength {
		add("WORKFLOW_NAME_TOO_LONG", "Workflow name is too long.", "name", nil, nil)
	}
	if workflow.DefinitionVersion != coreDB.WorkflowDefinitionVersion {
		add("WORKFLOW_SCHEMA_UNSUPPORTED", "Workflow definition version is not supported.", "definition_version", nil, nil)
	}
	if workflow.MaxParallelTasks < 1 || workflow.MaxParallelTasks > MaxWorkflowParallelism {
		add("WORKFLOW_PARALLELISM_INVALID", fmt.Sprintf("Workflow parallelism must be between 1 and %d.", MaxWorkflowParallelism), "max_parallel_tasks", nil, nil)
	}
	if len(workflow.Nodes) == 0 {
		add("WORKFLOW_NODES_REQUIRED", "At least one workflow node is required.", "nodes", nil, nil)
	}
	if len(workflow.Nodes) > MaxWorkflowNodes {
		add("WORKFLOW_NODE_LIMIT_EXCEEDED", fmt.Sprintf("A workflow can contain at most %d nodes.", MaxWorkflowNodes), "nodes", nil, nil)
	}
	if len(workflow.Edges) > MaxWorkflowEdges {
		add("WORKFLOW_EDGE_LIMIT_EXCEEDED", fmt.Sprintf("A workflow can contain at most %d edges.", MaxWorkflowEdges), "edges", nil, nil)
	}

	nodes := make(map[int]coreDB.WorkflowNode, len(workflow.Nodes))
	executable := make(map[int]struct{}, len(workflow.Nodes))
	resourceStore, hasResourceStore := store.(coreDB.WorkflowParameterValidationStore)
	requiresParameterResources := false
	parameterTypes := make(map[string]coreDB.WorkflowParameterType, len(workflow.ParameterDefinitions))
	for _, parameter := range workflow.ParameterDefinitions {
		requiresParameterResources = requiresParameterResources || len(parameter.SecretOptions) > 0
		parameterTypes[parameter.Name] = parameter.Type
	}
	if requiresParameterResources && !hasResourceStore {
		add("WORKFLOW_PARAMETER_RESOURCES_UNAVAILABLE", "Workflow parameter resources cannot be validated.", "parameters", nil, nil)
	}
	if hasResourceStore {
		for parameterIndex, parameter := range workflow.ParameterDefinitions {
			for optionIndex, option := range parameter.SecretOptions {
				key, err := resourceStore.GetAccessKey(workflow.ProjectID, option.AccessKeyID)
				if errors.Is(err, coreDB.ErrNotFound) || err == nil && (key.Type != coreDB.AccessKeyString || key.Owner != coreDB.AccessKeyShared) {
					add("WORKFLOW_PARAMETER_SECRET_NOT_APPROVED", "Secret reference is not an approved string credential in this project.",
						fmt.Sprintf("parameters[%d].secret_options[%d]", parameterIndex, optionIndex), nil, nil)
				} else if err != nil {
					return coreDB.WorkflowValidationResult{}, fmt.Errorf("validate workflow secret reference: %w", err)
				}
			}
		}
	}
	for index, node := range workflow.Nodes {
		id := node.ID
		path := fmt.Sprintf("nodes[%d]", index)
		if id == 0 {
			add("WORKFLOW_NODE_ID_REQUIRED", "Workflow node ID must not be zero.", path+".id", &id, nil)
		} else if _, exists := nodes[id]; exists {
			add("WORKFLOW_NODE_ID_DUPLICATE", "Workflow node ID must be unique.", path+".id", &id, nil)
		}
		nodes[id] = node
		if err := node.Kind.Validate(); err != nil {
			add("WORKFLOW_NODE_KIND_INVALID", "Workflow node kind is invalid.", path+".kind", &id, nil)
		}
		if node.EffectiveKind() != coreDB.WorkflowNodeNoteKind {
			executable[id] = struct{}{}
			if err := node.ConvergenceMode.Validate(); err != nil {
				add("WORKFLOW_CONVERGENCE_INVALID", "Workflow convergence mode is invalid.", path+".convergence_mode", &id, nil)
			}
			if err := node.EffectiveJoinMode().Validate(); err != nil {
				add("WORKFLOW_JOIN_MODE_INVALID", "Workflow join mode is invalid.", path+".join_mode", &id, nil)
			}
		}
		if len(node.DisplayName) > maxWorkflowLabelLength {
			add("WORKFLOW_NODE_DISPLAY_NAME_TOO_LONG", "Workflow node display name is too long.", path+".display_name", &id, nil)
		}
		switch node.EffectiveKind() {
		case coreDB.WorkflowNodeTaskKind:
			if node.CrossProjectTemplateReference != nil {
				if err := node.CrossProjectTemplateReference.ValidateNormalized(); err != nil {
					add("WORKFLOW_CROSS_PROJECT_REFERENCE_INVALID", "Cross-project template provenance is invalid.", path+".cross_project_template_reference", &id, nil)
				}
				if node.TemplateID != node.CrossProjectTemplateReference.TemplateID {
					add("WORKFLOW_CROSS_PROJECT_TEMPLATE_MISMATCH", "Cross-project provenance must match the task template.", path+".template_id", &id, nil)
				}
				if len(node.OverridePolicy.InventoryIDs) > 0 || len(node.OverridePolicy.EnvironmentIDs) > 0 || len(node.OverridePolicy.CredentialParameters) > 0 {
					add("WORKFLOW_CROSS_PROJECT_RESOURCE_OVERRIDE_FORBIDDEN", "Cross-project templates cannot override inventory, environments, or credentials.", path+".override_policy", &id, nil)
				}
				continue
			}
			for optionIndex, name := range node.OverridePolicy.CredentialParameters {
				if parameterTypes[name] != coreDB.WorkflowParameterSecretReference {
					add("WORKFLOW_NODE_CREDENTIAL_PARAMETER_INVALID", "Approved credential must reference a secret-reference workflow parameter.", fmt.Sprintf("%s.override_policy.credential_parameters[%d]", path, optionIndex), &id, nil)
				}
			}
			var template coreDB.Template
			templateAvailable := false
			if node.TemplateID <= 0 {
				add("WORKFLOW_TEMPLATE_REQUIRED", "Task nodes require a template.", path+".template_id", &id, nil)
			} else if store != nil {
				var err error
				template, err = store.GetTemplate(workflow.ProjectID, node.TemplateID)
				if errors.Is(err, coreDB.ErrNotFound) {
					add("WORKFLOW_TEMPLATE_NOT_IN_PROJECT", "Template is not available in this project.", path+".template_id", &id, nil)
				} else if err != nil {
					return coreDB.WorkflowValidationResult{}, fmt.Errorf("validate workflow template reference: %w", err)
				} else {
					templateAvailable = true
				}
			}
			if templateAvailable {
				if len(node.OverridePolicy.InventoryIDs) > 0 {
					allowed, err := template.CanOverrideInventory()
					if err != nil {
						return coreDB.WorkflowValidationResult{}, fmt.Errorf("validate workflow inventory override policy: %w", err)
					}
					if !allowed {
						add("WORKFLOW_NODE_INVENTORY_OVERRIDE_FORBIDDEN", "The task template does not allow inventory overrides.", path+".override_policy.inventory_ids", &id, nil)
					}
				}
				if node.OverridePolicy.AllowArguments && !template.AllowOverrideArgsInTask {
					add("WORKFLOW_NODE_ARGUMENTS_OVERRIDE_FORBIDDEN", "The task template does not allow argument overrides.", path+".override_policy.allow_arguments", &id, nil)
				}
				if node.OverridePolicy.AllowBranch && !template.AllowOverrideBranchInTask {
					add("WORKFLOW_NODE_BRANCH_OVERRIDE_FORBIDDEN", "The task template does not allow branch overrides.", path+".override_policy.allow_branch", &id, nil)
				}
			}
			if len(node.OverridePolicy.InventoryIDs) > 0 || len(node.OverridePolicy.EnvironmentIDs) > 0 {
				if !hasResourceStore {
					add("WORKFLOW_NODE_OVERRIDE_RESOURCES_UNAVAILABLE", "Workflow node override resources cannot be validated.", path+".override_policy", &id, nil)
				} else {
					for optionIndex, inventoryID := range node.OverridePolicy.InventoryIDs {
						if _, err := resourceStore.GetInventory(workflow.ProjectID, inventoryID); errors.Is(err, coreDB.ErrNotFound) {
							add("WORKFLOW_NODE_INVENTORY_NOT_IN_PROJECT", "Approved inventory is not available in this project.", fmt.Sprintf("%s.override_policy.inventory_ids[%d]", path, optionIndex), &id, nil)
						} else if err != nil {
							return coreDB.WorkflowValidationResult{}, fmt.Errorf("validate workflow inventory reference: %w", err)
						}
					}
					for optionIndex, environmentID := range node.OverridePolicy.EnvironmentIDs {
						if _, err := resourceStore.GetEnvironment(workflow.ProjectID, environmentID); errors.Is(err, coreDB.ErrNotFound) {
							add("WORKFLOW_NODE_ENVIRONMENT_NOT_IN_PROJECT", "Approved environment is not available in this project.", fmt.Sprintf("%s.override_policy.environment_ids[%d]", path, optionIndex), &id, nil)
						} else if err != nil {
							return coreDB.WorkflowValidationResult{}, fmt.Errorf("validate workflow environment reference: %w", err)
						}
					}
				}
			}
		case coreDB.WorkflowNodeApprovalKind:
			if node.TemplateID != 0 {
				add("WORKFLOW_APPROVAL_TEMPLATE_FORBIDDEN", "Approval nodes cannot reference a template.", path+".template_id", &id, nil)
			}
			if node.ApprovalTimeout != nil && *node.ApprovalTimeout <= 0 {
				add("WORKFLOW_APPROVAL_TIMEOUT_INVALID", "Approval timeout must be positive.", path+".approval_timeout", &id, nil)
			}
			if len(stringValue(node.ApprovalMessage)) > coreDB.MaxWorkflowApprovalPromptBytes {
				add("WORKFLOW_APPROVAL_MESSAGE_TOO_LONG", "Approval message is too long.", path+".approval_message", &id, nil)
			}
			permission := node.EffectiveApprovalPermission()
			if !validWorkflowApprovalPermission(permission) {
				add("WORKFLOW_APPROVAL_PERMISSION_INVALID", "Approval permission must be one supported project permission.", path+".approval_permission", &id, nil)
			}
			if err := node.EffectiveApprovalTimeoutOutcome().Validate(); err != nil {
				add("WORKFLOW_APPROVAL_TIMEOUT_OUTCOME_INVALID", "Approval timeout outcome is invalid.", path+".approval_timeout_outcome", &id, nil)
			}
		case coreDB.WorkflowNodeNoteKind:
			if node.TemplateID != 0 {
				add("WORKFLOW_NOTE_TEMPLATE_FORBIDDEN", "Note nodes cannot reference a template.", path+".template_id", &id, nil)
			}
		}
	}

	edgeIDs := make(map[int]struct{}, len(workflow.Edges))
	adjacency := make(map[int][]int, len(executable))
	undirected := make(map[int][]int, len(executable))
	incoming := make(map[int]int, len(executable))
	for index, edge := range workflow.Edges {
		id := edge.ID
		path := fmt.Sprintf("edges[%d]", index)
		if id == 0 {
			add("WORKFLOW_EDGE_ID_REQUIRED", "Workflow edge ID must not be zero.", path+".id", nil, &id)
		} else if _, exists := edgeIDs[id]; exists {
			add("WORKFLOW_EDGE_ID_DUPLICATE", "Workflow edge ID must be unique.", path+".id", nil, &id)
		}
		edgeIDs[id] = struct{}{}
		if err := edge.Condition.Validate(); err != nil {
			add("WORKFLOW_EDGE_CONDITION_INVALID", "Workflow edge condition is invalid.", path+".condition", nil, &id)
		}
		if len(edge.Label) > maxWorkflowLabelLength {
			add("WORKFLOW_EDGE_LABEL_TOO_LONG", "Workflow edge label is too long.", path+".label", nil, &id)
		}
		_, sourceExists := nodes[edge.SourceNodeID]
		_, destinationExists := nodes[edge.DestinationNodeID]
		if !sourceExists {
			add("WORKFLOW_EDGE_SOURCE_MISSING", "Workflow edge source does not exist.", path+".source_node_id", nil, &id)
		}
		if !destinationExists {
			add("WORKFLOW_EDGE_DESTINATION_MISSING", "Workflow edge destination does not exist.", path+".destination_node_id", nil, &id)
		}
		if edge.SourceNodeID == edge.DestinationNodeID {
			add("WORKFLOW_SELF_EDGE", "A workflow node cannot connect to itself.", path, nil, &id)
		}
		_, sourceExecutable := executable[edge.SourceNodeID]
		_, destinationExecutable := executable[edge.DestinationNodeID]
		if sourceExists && destinationExists && (!sourceExecutable || !destinationExecutable) {
			add("WORKFLOW_NOTE_EDGE_FORBIDDEN", "Note nodes cannot be connected.", path, nil, &id)
		}
		if sourceExecutable && destinationExecutable && edge.SourceNodeID != edge.DestinationNodeID {
			adjacency[edge.SourceNodeID] = append(adjacency[edge.SourceNodeID], edge.DestinationNodeID)
			undirected[edge.SourceNodeID] = append(undirected[edge.SourceNodeID], edge.DestinationNodeID)
			undirected[edge.DestinationNodeID] = append(undirected[edge.DestinationNodeID], edge.SourceNodeID)
			incoming[edge.DestinationNodeID]++
		}
	}

	if len(executable) > 0 {
		roots := 0
		var first int
		for id := range executable {
			if first == 0 {
				first = id
			}
			if incoming[id] == 0 {
				roots++
			}
		}
		if roots != 1 {
			add("WORKFLOW_ROOT_COUNT_INVALID", "Workflow must have exactly one starting node.", "nodes", nil, nil)
		}
		if hasCycle(adjacency, executable) {
			add("WORKFLOW_CYCLE", "Workflow graph must not contain a cycle.", "edges", nil, nil)
		}
		seen := map[int]struct{}{first: {}}
		stack := []int{first}
		for len(stack) > 0 {
			current := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, next := range undirected[current] {
				if _, exists := seen[next]; exists {
					continue
				}
				seen[next] = struct{}{}
				stack = append(stack, next)
			}
		}
		if len(seen) != len(executable) {
			add("WORKFLOW_DISCONNECTED", "Workflow graph contains disconnected nodes.", "nodes", nil, nil)
		}
	}

	return coreDB.WorkflowValidationResult{Valid: len(issues) == 0, Issues: issues}, nil
}

func trimWorkflowApprovalMessage(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func validWorkflowApprovalPermission(permission coreDB.ProjectUserPermission) bool {
	switch permission {
	case coreDB.CanRunProjectTasks,
		coreDB.CanUpdateProject,
		coreDB.CanManageProjectResources,
		coreDB.CanManageProjectUsers:
		return true
	default:
		return false
	}
}

func compileWorkflowConditions(workflow *coreDB.WorkflowTemplate) []coreDB.WorkflowValidationIssue {
	issues := make([]coreDB.WorkflowValidationIssue, 0)
	for index := range workflow.Edges {
		edge := &workflow.Edges[index]
		expression := edge.Expression
		if expression == "" {
			switch edge.Condition {
			case coreDB.WorkflowEdgeOnSuccess:
				expression = `result.status == "succeeded"`
			case coreDB.WorkflowEdgeOnFailure:
				expression = `result.status == "failed" || result.status == "canceled" || result.status == "stopped"`
			case coreDB.WorkflowEdgeAlways:
				expression = "true"
			case coreDB.WorkflowEdgeExpression:
				issues = append(issues, workflowConditionIssue(index, edge.ID, "Condition expression is required."))
				continue
			default:
				continue
			}
		}
		program, err := CompileWorkflowCondition(expression)
		if err != nil {
			issues = append(issues, workflowConditionIssue(index, edge.ID, err.Error()))
			continue
		}
		encoded, err := json.Marshal(program)
		if err != nil {
			issues = append(issues, workflowConditionIssue(index, edge.ID, "Condition program could not be stored."))
			continue
		}
		edge.ConditionProgram = program
		edge.ConditionProgramJSON = string(encoded)
	}
	return issues
}

func workflowConditionIssue(index, edgeID int, message string) coreDB.WorkflowValidationIssue {
	return coreDB.WorkflowValidationIssue{
		Code: "WORKFLOW_EDGE_EXPRESSION_INVALID", Message: message,
		Path: fmt.Sprintf("edges[%d].condition_expression", index), EdgeID: &edgeID,
	}
}

func hasCycle(adjacency map[int][]int, nodes map[int]struct{}) bool {
	state := make(map[int]uint8, len(nodes))
	var visit func(int) bool
	visit = func(node int) bool {
		if state[node] == 1 {
			return true
		}
		if state[node] == 2 {
			return false
		}
		state[node] = 1
		for _, next := range adjacency[node] {
			if visit(next) {
				return true
			}
		}
		state[node] = 2
		return false
	}
	for node := range nodes {
		if visit(node) {
			return true
		}
	}
	return false
}

func WorkflowConditionMatches(status coreDB.WorkflowRunStatus, condition coreDB.WorkflowEdgeCondition) bool {
	switch condition {
	case coreDB.WorkflowEdgeAlways:
		return true
	case coreDB.WorkflowEdgeOnSuccess:
		return status == coreDB.WorkflowRunSucceeded || status == coreDB.WorkflowRunSuccess
	case coreDB.WorkflowEdgeOnFailure:
		return status == coreDB.WorkflowRunFailed || status == coreDB.WorkflowRunCanceled || status == coreDB.WorkflowRunStopped
	default:
		return false
	}
}

func WorkflowRootNode(workflow coreDB.WorkflowTemplate) (coreDB.WorkflowNode, error) {
	incoming := make(map[int]struct{}, len(workflow.Edges))
	for _, edge := range workflow.Edges {
		incoming[edge.DestinationNodeID] = struct{}{}
	}
	var root *coreDB.WorkflowNode
	for index := range workflow.Nodes {
		node := workflow.Nodes[index]
		if node.EffectiveKind() == coreDB.WorkflowNodeNoteKind {
			continue
		}
		if _, exists := incoming[node.ID]; exists {
			continue
		}
		if root != nil {
			return coreDB.WorkflowNode{}, errors.New("workflow has multiple root nodes")
		}
		root = &node
	}
	if root == nil {
		return coreDB.WorkflowNode{}, errors.New("workflow has no root node")
	}
	return *root, nil
}

// BuildWorkflowRunSnapshot freezes the definition and every referenced task
// template before any task is created.
func BuildWorkflowRunSnapshot(
	workflow coreDB.WorkflowTemplate,
	templates map[int]coreDB.Template,
	actorUserID int,
	correlationID string,
	now time.Time,
	inputs ...coreDB.WorkflowRunInput,
) (coreDB.WorkflowRun, error) {
	return BuildWorkflowRunSnapshotWithCrossProjectProvenance(workflow, templates, nil, actorUserID, correlationID, now, inputs...)
}

// BuildWorkflowRunSnapshotWithCrossProjectProvenance freezes local templates
// and server-resolved external template versions in one immutable run graph.
// The caller supplies provenance only after validating a live run grant.
func BuildWorkflowRunSnapshotWithCrossProjectProvenance(
	workflow coreDB.WorkflowTemplate,
	templates map[int]coreDB.Template,
	crossProject map[int]coreDB.CrossProjectTemplateProvenance,
	actorUserID int,
	correlationID string,
	now time.Time,
	inputs ...coreDB.WorkflowRunInput,
) (coreDB.WorkflowRun, error) {
	workflow = NormalizeWorkflowTemplate(workflow)
	if issues := compileWorkflowConditions(&workflow); len(issues) > 0 {
		return coreDB.WorkflowRun{}, common_errors.NewValidationError(issues[0].Message)
	}
	if actorUserID <= 0 {
		return coreDB.WorkflowRun{}, common_errors.NewValidationError("workflow run actor is required")
	}
	if strings.TrimSpace(correlationID) == "" {
		return coreDB.WorkflowRun{}, common_errors.NewValidationError("workflow run correlation ID is required")
	}
	if err := validateRunnableWorkflow(workflow); err != nil {
		return coreDB.WorkflowRun{}, err
	}
	var input coreDB.WorkflowRunInput
	if len(inputs) > 1 {
		return coreDB.WorkflowRun{}, common_errors.NewValidationError("workflow run input is invalid")
	}
	if len(inputs) == 1 {
		input = inputs[0]
	}
	parameterSnapshot, err := coreDB.ResolveWorkflowParameters(
		workflow.ParameterDefinitions, input.TriggerValues, input.UserValues,
	)
	if err != nil {
		return coreDB.WorkflowRun{}, common_errors.NewValidationError(err.Error())
	}
	parameterJSON, err := json.Marshal(parameterSnapshot)
	if err != nil {
		return coreDB.WorkflowRun{}, fmt.Errorf("snapshot workflow parameters: %w", err)
	}
	triggerSnapshotJSON := "{}"
	var triggerSnapshot coreDB.WorkflowTriggerSnapshot
	if input.TriggerSnapshot != nil {
		triggerSnapshot = *input.TriggerSnapshot
		encodedTrigger, marshalErr := json.Marshal(triggerSnapshot)
		if marshalErr != nil {
			return coreDB.WorkflowRun{}, fmt.Errorf("snapshot workflow trigger: %w", marshalErr)
		}
		triggerSnapshotJSON = string(encodedTrigger)
	}
	definitionNodes := make(map[int]coreDB.WorkflowNode, len(workflow.Nodes))
	for _, node := range workflow.Nodes {
		definitionNodes[node.ID] = node
	}
	for nodeID := range input.NodeOverrides {
		if _, exists := definitionNodes[nodeID]; !exists {
			return coreDB.WorkflowRun{}, common_errors.NewValidationError(fmt.Sprintf("workflow node override %d is unknown", nodeID))
		}
	}
	definitionJSON, err := json.Marshal(workflow)
	if err != nil {
		return coreDB.WorkflowRun{}, fmt.Errorf("snapshot workflow definition: %w", err)
	}
	run := coreDB.WorkflowRun{
		ProjectID: workflow.ProjectID, WorkflowTemplateID: workflow.ID,
		Status: coreDB.WorkflowRunPending, ActorUserID: actorUserID,
		DefinitionVersion: workflow.DefinitionVersion, DefinitionRevision: workflow.Revision,
		WorkflowVersionID: workflow.CurrentVersionID,
		CorrelationID:     correlationID, DefinitionSnapshotJSON: string(definitionJSON),
		DefinitionSnapshot: workflow, ParameterSnapshotJSON: string(parameterJSON), ParameterSnapshot: parameterSnapshot,
		TriggerSnapshotJSON: triggerSnapshotJSON, TriggerSnapshot: triggerSnapshot,
		Created: now, Start: &now,
		Nodes: make([]coreDB.WorkflowRunNode, 0, len(workflow.Nodes)),
	}
	for _, node := range workflow.Nodes {
		if node.EffectiveKind() == coreDB.WorkflowNodeNoteKind {
			continue
		}
		var template coreDB.Template
		var provenance *coreDB.CrossProjectTemplateProvenance
		if node.EffectiveKind() == coreDB.WorkflowNodeTaskKind {
			if node.CrossProjectTemplateReference != nil {
				resolved, ok := crossProject[node.ID]
				if !ok || resolved.Reference != *node.CrossProjectTemplateReference || resolved.Validate() != nil {
					return coreDB.WorkflowRun{}, common_errors.NewValidationError("workflow cross-project template provenance is unavailable")
				}
				copy := resolved
				provenance = &copy
			} else {
				var ok bool
				template, ok = templates[node.TemplateID]
				if !ok || template.ID == 0 || template.ProjectID != workflow.ProjectID {
					return coreDB.WorkflowRun{}, common_errors.NewValidationError("workflow task template snapshot is unavailable")
				}
			}
		}
		override := input.NodeOverrides[node.ID]
		if err := coreDB.ValidateWorkflowNodeOverride(node.OverridePolicy, override); err != nil {
			return coreDB.WorkflowRun{}, common_errors.NewValidationError(fmt.Sprintf("workflow node %d override: %s", node.ID, err.Error()))
		}
		if provenance != nil && (override.InventoryID != nil || override.EnvironmentIDs != nil) {
			return coreDB.WorkflowRun{}, common_errors.NewValidationError("workflow cross-project template resource overrides are forbidden")
		}
		if override.EnvironmentIDs != nil {
			template.EnvironmentIDs = append([]int(nil), (*override.EnvironmentIDs)...)
		}
		templateJSON, marshalErr := json.Marshal(template)
		if marshalErr != nil {
			return coreDB.WorkflowRun{}, fmt.Errorf("snapshot workflow task template: %w", marshalErr)
		}
		overrideJSON, marshalErr := json.Marshal(override)
		if marshalErr != nil {
			return coreDB.WorkflowRun{}, fmt.Errorf("snapshot workflow node override: %w", marshalErr)
		}
		run.Nodes = append(run.Nodes, coreDB.WorkflowRunNode{
			ProjectID: workflow.ProjectID, WorkflowNodeID: node.ID, TemplateID: node.TemplateID,
			Status: coreDB.WorkflowRunNodePending, TemplateSnapshotJSON: string(templateJSON),
			TemplateSnapshot: template, CrossProjectTemplateProvenance: provenance,
			OverrideSnapshotJSON: string(overrideJSON), OverrideSnapshot: override, Created: now,
		})
	}
	return run, nil
}

func validateRunnableWorkflow(workflow coreDB.WorkflowTemplate) error {
	executable := 0
	for _, node := range workflow.Nodes {
		if node.EffectiveKind() == coreDB.WorkflowNodeNoteKind {
			continue
		}
		if node.EffectiveKind() != coreDB.WorkflowNodeTaskKind && node.EffectiveKind() != coreDB.WorkflowNodeApprovalKind {
			return common_errors.NewValidationError("workflow run node kind is invalid")
		}
		executable++
	}
	if executable == 0 {
		return common_errors.NewValidationError("workflow run requires at least one task node")
	}
	if workflow.MaxParallelTasks < 1 || workflow.MaxParallelTasks > MaxWorkflowParallelism {
		return common_errors.NewValidationError("workflow parallelism is invalid")
	}
	for _, edge := range workflow.Edges {
		if edge.ConditionProgram.Version != workflowConditionProgramVersion || len(edge.ConditionProgram.Instructions) == 0 {
			return common_errors.NewValidationError("workflow edge condition program is unavailable")
		}
	}
	if _, err := WorkflowRootNode(workflow); err != nil {
		return err
	}
	return nil
}

func WorkflowRunNodeStatusFromTaskStatus(status task_logger.TaskStatus) coreDB.WorkflowRunNodeStatus {
	switch status {
	case task_logger.TaskWaitingStatus, task_logger.TaskStartingStatus, task_logger.TaskWaitingConfirmation, task_logger.TaskConfirmed:
		return coreDB.WorkflowRunNodeQueued
	case task_logger.TaskRunningStatus, task_logger.TaskStoppingStatus:
		return coreDB.WorkflowRunNodeRunning
	case task_logger.TaskSuccessStatus:
		return coreDB.WorkflowRunNodeSucceeded
	case task_logger.TaskStoppedStatus:
		return coreDB.WorkflowRunNodeCanceled
	case task_logger.TaskFailStatus, task_logger.TaskRejected:
		return coreDB.WorkflowRunNodeFailed
	default:
		return coreDB.WorkflowRunNodePending
	}
}
