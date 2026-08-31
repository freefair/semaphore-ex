package pro_interfaces

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/semaphoreui/semaphore/db"
)

const workflowDefinitionFingerprintPrefix = "sha256:"

// WorkflowDefinitionDiffChange is one bounded structural section change.
// Before and After contain definition metadata only; runtime values and
// credential material are not part of workflow definitions.
type WorkflowDefinitionDiffChange struct {
	Section string          `json:"section"`
	Before  json.RawMessage `json:"before,omitempty"`
	After   json.RawMessage `json:"after,omitempty"`
}

type WorkflowDefinitionDiff struct {
	Changes []WorkflowDefinitionDiffChange `json:"changes"`
}

func (diff WorkflowDefinitionDiff) ChangedSections() []string {
	sections := make([]string, 0, len(diff.Changes))
	for _, change := range diff.Changes {
		sections = append(sections, change.Section)
	}
	return sections
}

type canonicalWorkflowDefinition struct {
	Metadata    canonicalWorkflowMetadata         `json:"metadata"`
	Nodes       []canonicalWorkflowNode           `json:"nodes"`
	Edges       []canonicalWorkflowEdge           `json:"edges"`
	Parameters  []db.WorkflowParameterDeclaration `json:"parameters"`
	Permissions canonicalWorkflowPermissions      `json:"permissions"`
	References  []canonicalWorkflowReference      `json:"references"`
}

type canonicalWorkflowMetadata struct {
	Name              string  `json:"name"`
	Description       *string `json:"description,omitempty"`
	StartVersion      *string `json:"start_version,omitempty"`
	DefinitionVersion int     `json:"definition_version"`
	MaxParallelTasks  int     `json:"max_parallel_tasks"`
}

type canonicalWorkflowNode struct {
	Ordinal                    int                               `json:"ordinal"`
	TemplateID                 int                               `json:"template_id,omitempty"`
	DisplayName                string                            `json:"display_name,omitempty"`
	Kind                       db.WorkflowNodeKind               `json:"kind"`
	ConvergenceMode            db.WorkflowConvergenceMode        `json:"convergence_mode"`
	JoinMode                   db.WorkflowJoinMode               `json:"join_mode"`
	ApprovalTimeout            *int                              `json:"approval_timeout,omitempty"`
	ApprovalMessage            *string                           `json:"approval_message,omitempty"`
	ApprovalPermission         db.ProjectUserPermission          `json:"approval_permission,omitempty"`
	ApprovalTimeoutOutcome     db.WorkflowApprovalTimeoutOutcome `json:"approval_timeout_outcome,omitempty"`
	ApprovalSeparationOfDuties bool                              `json:"approval_separation_of_duties,omitempty"`
	TaskParams                 *db.TaskParams                    `json:"task_params,omitempty"`
	ArtifactOutputs            []db.WorkflowArtifactDeclaration  `json:"artifact_outputs,omitempty"`
	ArtifactInputs             []canonicalWorkflowArtifactInput  `json:"artifact_inputs,omitempty"`
	OverridePolicy             db.WorkflowNodeOverridePolicy     `json:"override_policy,omitempty"`
	Note                       *string                           `json:"note,omitempty"`
	PositionX                  int                               `json:"position_x"`
	PositionY                  int                               `json:"position_y"`
}

type canonicalWorkflowArtifactInput struct {
	Name          string `json:"name"`
	SourceOrdinal int    `json:"source_ordinal"`
	Output        string `json:"output"`
	Required      bool   `json:"required"`
}

type canonicalWorkflowEdge struct {
	SourceOrdinal      int                      `json:"source_ordinal"`
	DestinationOrdinal int                      `json:"destination_ordinal"`
	Condition          db.WorkflowEdgeCondition `json:"condition"`
	Expression         string                   `json:"condition_expression,omitempty"`
	Label              string                   `json:"label,omitempty"`
}

type canonicalWorkflowPermissions struct {
	ViewRoleIDs  []db.ProjectRoleReference             `json:"view_role_ids,omitempty"`
	StartRoleIDs []db.ProjectRoleReference             `json:"start_role_ids,omitempty"`
	Approvals    []canonicalWorkflowApprovalPermission `json:"approvals,omitempty"`
}

type canonicalWorkflowApprovalPermission struct {
	NodeOrdinal              int                         `json:"node_ordinal"`
	Mode                     db.WorkflowApprovalRoleMode `json:"mode"`
	RoleIDs                  []db.ProjectRoleReference   `json:"role_ids"`
	MinimumDistinctApprovers int                         `json:"minimum_distinct_approvers"`
	InitiatorSeparation      bool                        `json:"initiator_separation"`
}

// canonicalWorkflowReference deliberately excludes execution snapshot data.
// Workflow definitions retain normalized immutable identity; run nodes carry
// the value-free version snapshot created at start.
type canonicalWorkflowReference struct {
	NodeOrdinal           int    `json:"node_ordinal"`
	GrantID               int    `json:"grant_id"`
	GrantRevision         int    `json:"grant_revision"`
	OwnerProjectID        int    `json:"owner_project_id"`
	TemplateID            int    `json:"template_id"`
	TemplateVersionID     int    `json:"template_version_id"`
	TemplateVersionNumber int    `json:"template_version_number"`
	ContentFingerprint    string `json:"content_fingerprint"`
}

func WorkflowDefinitionFingerprint(workflow db.WorkflowTemplate) (string, error) {
	definition, err := canonicalizeWorkflowDefinition(workflow)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("encode canonical workflow definition: %w", err)
	}
	digest := sha256.Sum256(payload)
	return workflowDefinitionFingerprintPrefix + hex.EncodeToString(digest[:]), nil
}

func DiffWorkflowDefinitions(before db.WorkflowTemplate, after db.WorkflowTemplate) (WorkflowDefinitionDiff, error) {
	left, err := canonicalizeWorkflowDefinition(before)
	if err != nil {
		return WorkflowDefinitionDiff{}, err
	}
	right, err := canonicalizeWorkflowDefinition(after)
	if err != nil {
		return WorkflowDefinitionDiff{}, err
	}
	sections := []struct {
		name        string
		beforeValue any
		afterValue  any
	}{
		{"metadata", left.Metadata, right.Metadata},
		{"nodes", left.Nodes, right.Nodes},
		{"edges", left.Edges, right.Edges},
		{"parameters", left.Parameters, right.Parameters},
		{"permissions", left.Permissions, right.Permissions},
		{"references", left.References, right.References},
	}
	diff := WorkflowDefinitionDiff{Changes: make([]WorkflowDefinitionDiffChange, 0, len(sections))}
	for _, section := range sections {
		leftJSON, marshalErr := json.Marshal(section.beforeValue)
		if marshalErr != nil {
			return WorkflowDefinitionDiff{}, fmt.Errorf("encode workflow %s before diff: %w", section.name, marshalErr)
		}
		rightJSON, marshalErr := json.Marshal(section.afterValue)
		if marshalErr != nil {
			return WorkflowDefinitionDiff{}, fmt.Errorf("encode workflow %s after diff: %w", section.name, marshalErr)
		}
		if string(leftJSON) == string(rightJSON) {
			continue
		}
		diff.Changes = append(diff.Changes, WorkflowDefinitionDiffChange{
			Section: section.name, Before: leftJSON, After: rightJSON,
		})
	}
	return diff, nil
}

func canonicalizeWorkflowDefinition(workflow db.WorkflowTemplate) (canonicalWorkflowDefinition, error) {
	ordinals := make(map[int]int, len(workflow.Nodes))
	for index, node := range workflow.Nodes {
		if _, duplicate := ordinals[node.ID]; duplicate {
			return canonicalWorkflowDefinition{}, fmt.Errorf("workflow node id %d is duplicated", node.ID)
		}
		ordinals[node.ID] = index + 1
	}
	nodes := make([]canonicalWorkflowNode, len(workflow.Nodes))
	approvals := make([]canonicalWorkflowApprovalPermission, 0)
	references := make([]canonicalWorkflowReference, 0)
	for index, node := range workflow.Nodes {
		inputs := make([]canonicalWorkflowArtifactInput, len(node.ArtifactInputs))
		for inputIndex, input := range node.ArtifactInputs {
			sourceOrdinal, exists := ordinals[input.SourceNodeID]
			if !exists {
				return canonicalWorkflowDefinition{}, fmt.Errorf("workflow artifact input references unknown node %d", input.SourceNodeID)
			}
			inputs[inputIndex] = canonicalWorkflowArtifactInput{
				Name: input.Name, SourceOrdinal: sourceOrdinal, Output: input.Output, Required: input.Required,
			}
		}
		nodes[index] = canonicalWorkflowNode{
			Ordinal: index + 1, TemplateID: node.TemplateID, DisplayName: node.DisplayName,
			Kind: node.EffectiveKind(), ConvergenceMode: node.EffectiveConvergenceMode(), JoinMode: node.EffectiveJoinMode(),
			ApprovalTimeout: node.ApprovalTimeout, ApprovalMessage: node.ApprovalMessage,
			ApprovalPermission: node.EffectiveApprovalPermission(), ApprovalTimeoutOutcome: node.EffectiveApprovalTimeoutOutcome(),
			ApprovalSeparationOfDuties: node.ApprovalSeparationOfDuties, TaskParams: node.TaskParams,
			ArtifactOutputs: append([]db.WorkflowArtifactDeclaration(nil), node.ArtifactOutputs...), ArtifactInputs: inputs,
			OverridePolicy: node.OverridePolicy, Note: node.Note, PositionX: node.PositionX, PositionY: node.PositionY,
		}
		if node.CrossProjectTemplateReference != nil {
			reference := node.CrossProjectTemplateReference
			if err := reference.ValidateNormalized(); err != nil {
				return canonicalWorkflowDefinition{}, fmt.Errorf("workflow cross-project reference: %w", err)
			}
			references = append(references, canonicalWorkflowReference{
				NodeOrdinal: index + 1, GrantID: reference.GrantID, GrantRevision: reference.GrantRevision,
				OwnerProjectID: reference.OwnerProjectID, TemplateID: reference.TemplateID,
				TemplateVersionID: reference.TemplateVersionID, TemplateVersionNumber: reference.TemplateVersionNumber,
				ContentFingerprint: reference.ContentFingerprint,
			})
		}
		if node.EffectiveKind() == db.WorkflowNodeApprovalKind && len(node.ApprovalRolePolicy.RoleIDs) > 0 {
			roles := append([]db.ProjectRoleReference(nil), node.ApprovalRolePolicy.RoleIDs...)
			sort.Slice(roles, func(i, j int) bool { return roles[i] < roles[j] })
			approvals = append(approvals, canonicalWorkflowApprovalPermission{
				NodeOrdinal: index + 1, Mode: node.ApprovalRolePolicy.Mode, RoleIDs: roles,
				MinimumDistinctApprovers: node.ApprovalRolePolicy.MinimumDistinctApprovers,
				InitiatorSeparation:      node.ApprovalRolePolicy.InitiatorSeparation,
			})
		}
	}
	edges := make([]canonicalWorkflowEdge, len(workflow.Edges))
	for index, edge := range workflow.Edges {
		sourceOrdinal, sourceExists := ordinals[edge.SourceNodeID]
		destinationOrdinal, destinationExists := ordinals[edge.DestinationNodeID]
		if !sourceExists || !destinationExists {
			return canonicalWorkflowDefinition{}, fmt.Errorf("workflow edge references an unknown node")
		}
		edges[index] = canonicalWorkflowEdge{
			SourceOrdinal: sourceOrdinal, DestinationOrdinal: destinationOrdinal,
			Condition: edge.Condition, Expression: edge.Expression, Label: edge.Label,
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].SourceOrdinal != edges[j].SourceOrdinal {
			return edges[i].SourceOrdinal < edges[j].SourceOrdinal
		}
		if edges[i].DestinationOrdinal != edges[j].DestinationOrdinal {
			return edges[i].DestinationOrdinal < edges[j].DestinationOrdinal
		}
		if edges[i].Condition != edges[j].Condition {
			return edges[i].Condition < edges[j].Condition
		}
		if edges[i].Expression != edges[j].Expression {
			return edges[i].Expression < edges[j].Expression
		}
		return edges[i].Label < edges[j].Label
	})
	viewRoles := append([]db.ProjectRoleReference(nil), workflow.AccessPolicy.ViewRoleIDs...)
	startRoles := append([]db.ProjectRoleReference(nil), workflow.AccessPolicy.StartRoleIDs...)
	sort.Slice(viewRoles, func(i, j int) bool { return viewRoles[i] < viewRoles[j] })
	sort.Slice(startRoles, func(i, j int) bool { return startRoles[i] < startRoles[j] })
	sort.Slice(references, func(i, j int) bool { return references[i].NodeOrdinal < references[j].NodeOrdinal })
	return canonicalWorkflowDefinition{
		Metadata: canonicalWorkflowMetadata{
			Name: workflow.Name, Description: workflow.Description, StartVersion: workflow.StartVersion,
			DefinitionVersion: workflow.DefinitionVersion, MaxParallelTasks: workflow.MaxParallelTasks,
		},
		Nodes: nodes, Edges: edges,
		Parameters:  append([]db.WorkflowParameterDeclaration(nil), workflow.ParameterDefinitions...),
		Permissions: canonicalWorkflowPermissions{ViewRoleIDs: viewRoles, StartRoleIDs: startRoles, Approvals: approvals},
		References:  references,
	}, nil
}
