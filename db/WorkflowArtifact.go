package db

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultWorkflowArtifactMaxBytes  = 16 * 1024
	MaxWorkflowArtifactBytes         = 64 * 1024
	MaxWorkflowArtifactObservedBytes = MaxWorkflowArtifactBytes + 1
	MaxWorkflowArtifactEventBytes    = MaxWorkflowArtifactsPerNode*MaxWorkflowArtifactBytes + 64*1024
	MaxWorkflowArtifactsPerNode      = 32
	MaxWorkflowArtifactInputs        = 64
	MaxWorkflowArtifactDiagnostic    = 1024
	maxWorkflowArtifactSchemaDepth   = 4
	maxWorkflowArtifactProperties    = 64
)

var workflowArtifactNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

type WorkflowArtifactValueType string

const (
	WorkflowArtifactString  WorkflowArtifactValueType = "string"
	WorkflowArtifactInteger WorkflowArtifactValueType = "integer"
	WorkflowArtifactNumber  WorkflowArtifactValueType = "number"
	WorkflowArtifactBoolean WorkflowArtifactValueType = "boolean"
	WorkflowArtifactObject  WorkflowArtifactValueType = "object"
	WorkflowArtifactArray   WorkflowArtifactValueType = "array"
)

// WorkflowArtifactSchema is the bounded JSON Schema subset accepted for
// workflow outputs. Objects are closed: a value may contain only declared
// properties. Arrays require one item schema.
type WorkflowArtifactSchema struct {
	Type       WorkflowArtifactValueType         `json:"type"`
	Properties map[string]WorkflowArtifactSchema `json:"properties,omitempty"`
	Required   []string                          `json:"required,omitempty"`
	Items      *WorkflowArtifactSchema           `json:"items,omitempty"`
}

type WorkflowArtifactDeclaration struct {
	Name      string                 `json:"name"`
	Schema    WorkflowArtifactSchema `json:"schema"`
	Sensitive bool                   `json:"sensitive"`
	MaxBytes  int                    `json:"max_bytes"`
}

type WorkflowArtifactReference struct {
	Name         string `json:"name"`
	SourceNodeID int    `json:"source_node_id"`
	Output       string `json:"output"`
	Required     bool   `json:"required"`
}

type WorkflowArtifactAvailability string

const (
	WorkflowArtifactAvailable   WorkflowArtifactAvailability = "available"
	WorkflowArtifactUnavailable WorkflowArtifactAvailability = "unavailable"
	WorkflowArtifactInvalid     WorkflowArtifactAvailability = "invalid"
)

// WorkflowArtifactInputSnapshot records how one task input was resolved. It
// deliberately contains no artifact value or value-derived fingerprint.
type WorkflowArtifactInputSnapshot struct {
	Name                 string                       `json:"name"`
	SourceNodeID         int                          `json:"source_node_id"`
	Output               string                       `json:"output"`
	Sensitive            bool                         `json:"sensitive"`
	Required             bool                         `json:"required"`
	Availability         WorkflowArtifactAvailability `json:"availability"`
	ReferenceFingerprint string                       `json:"reference_fingerprint"`
	ProducerTaskID       *int                         `json:"producer_task_id,omitempty"`
	ProducerAttempt      *int                         `json:"producer_attempt,omitempty"`
}

// WorkflowArtifact is one output captured for an exact workflow task
// assignment. Plaintext and ciphertext fields are internal-only; API models
// must expose the remaining provenance and availability metadata instead.
type WorkflowArtifact struct {
	ID             int                          `db:"id" json:"id"`
	ProjectID      int                          `db:"project_id" json:"project_id"`
	WorkflowRunID  int                          `db:"workflow_run_id" json:"workflow_run_id"`
	WorkflowNodeID int                          `db:"workflow_node_id" json:"workflow_node_id"`
	TaskID         int                          `db:"task_id" json:"task_id"`
	Attempt        int                          `db:"attempt" json:"attempt"`
	Name           string                       `db:"name" json:"name"`
	SchemaJSON     string                       `db:"schema" json:"-"`
	Schema         WorkflowArtifactSchema       `db:"-" json:"schema"`
	Sensitive      bool                         `db:"sensitive" json:"sensitive"`
	Availability   WorkflowArtifactAvailability `db:"availability" json:"availability"`
	SizeBytes      int                          `db:"size_bytes" json:"size_bytes"`
	Fingerprint    string                       `db:"reference_fingerprint" json:"reference_fingerprint"`
	Diagnostic     string                       `db:"diagnostic" json:"diagnostic,omitempty"`
	ValueJSON      string                       `db:"value_json" json:"-"`
	EncryptedValue string                       `db:"encrypted_value" json:"-"`
}

// WorkflowArtifactMetadata is the value-free API representation of a declared
// output for the current producer attempt.
type WorkflowArtifactMetadata struct {
	WorkflowNodeID  int                          `json:"workflow_node_id"`
	Name            string                       `json:"name"`
	Schema          WorkflowArtifactSchema       `json:"schema"`
	Sensitive       bool                         `json:"sensitive"`
	Availability    WorkflowArtifactAvailability `json:"availability"`
	SizeBytes       int                          `json:"size_bytes"`
	Fingerprint     string                       `json:"reference_fingerprint"`
	Diagnostic      string                       `json:"diagnostic,omitempty"`
	ProducerTaskID  *int                         `json:"producer_task_id,omitempty"`
	ProducerAttempt *int                         `json:"producer_attempt,omitempty"`
}

func (declaration WorkflowArtifactDeclaration) Validate() error {
	if err := ValidateWorkflowArtifactName(declaration.Name); err != nil {
		return err
	}
	if declaration.MaxBytes < 1 || declaration.MaxBytes > MaxWorkflowArtifactBytes {
		return fmt.Errorf("workflow artifact maximum size must be between 1 and %d bytes", MaxWorkflowArtifactBytes)
	}
	return declaration.Schema.Validate()
}

func ValidateWorkflowArtifactName(name string) error {
	if !workflowArtifactNamePattern.MatchString(name) {
		return errors.New("workflow artifact name is invalid")
	}
	return nil
}

func (reference WorkflowArtifactReference) Validate() error {
	if !workflowArtifactNamePattern.MatchString(reference.Name) {
		return errors.New("workflow artifact input name is invalid")
	}
	if reference.SourceNodeID == 0 {
		return errors.New("workflow artifact source node is required")
	}
	if !workflowArtifactNamePattern.MatchString(reference.Output) {
		return errors.New("workflow artifact output name is invalid")
	}
	return nil
}

func (schema WorkflowArtifactSchema) Validate() error {
	return schema.validate(1)
}

func (schema WorkflowArtifactSchema) validate(depth int) error {
	if depth > maxWorkflowArtifactSchemaDepth {
		return fmt.Errorf("workflow artifact schema exceeds maximum depth %d", maxWorkflowArtifactSchemaDepth)
	}
	switch schema.Type {
	case WorkflowArtifactString, WorkflowArtifactInteger, WorkflowArtifactNumber, WorkflowArtifactBoolean:
		if len(schema.Properties) != 0 || len(schema.Required) != 0 || schema.Items != nil {
			return errors.New("workflow artifact scalar schema cannot declare properties or items")
		}
		return nil
	case WorkflowArtifactObject:
		if schema.Items != nil {
			return errors.New("workflow artifact object schema cannot declare items")
		}
		if len(schema.Properties) > maxWorkflowArtifactProperties {
			return fmt.Errorf("workflow artifact object schema exceeds %d properties", maxWorkflowArtifactProperties)
		}
		for _, name := range sortedWorkflowArtifactKeys(schema.Properties) {
			property := schema.Properties[name]
			if !workflowArtifactNamePattern.MatchString(name) {
				return fmt.Errorf("workflow artifact schema property %q is invalid", name)
			}
			if err := property.validate(depth + 1); err != nil {
				return fmt.Errorf("workflow artifact schema property %q: %w", name, err)
			}
		}
		seen := make(map[string]struct{}, len(schema.Required))
		for _, name := range schema.Required {
			if _, exists := schema.Properties[name]; !exists {
				return fmt.Errorf("workflow artifact required property %q is not declared", name)
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("workflow artifact required property %q is duplicated", name)
			}
			seen[name] = struct{}{}
		}
		return nil
	case WorkflowArtifactArray:
		if len(schema.Properties) != 0 || len(schema.Required) != 0 || schema.Items == nil {
			return errors.New("workflow artifact array schema requires items and cannot declare properties")
		}
		return schema.Items.validate(depth + 1)
	default:
		return errors.New("workflow artifact schema type is invalid")
	}
}

func ValidateWorkflowArtifactValue(schema WorkflowArtifactSchema, raw json.RawMessage, maxBytes int) error {
	if err := schema.Validate(); err != nil {
		return err
	}
	if maxBytes < 1 || maxBytes > MaxWorkflowArtifactBytes {
		return errors.New("workflow artifact maximum size is invalid")
	}
	if len(raw) == 0 || len(raw) > maxBytes {
		return fmt.Errorf("workflow artifact value exceeds %d bytes", maxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return errors.New("workflow artifact value is not valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("workflow artifact value contains trailing JSON data")
	}
	return validateWorkflowArtifactJSON(schema, value, "$", 1)
}

// ValidateWorkflowArtifactGraph validates declaration shape and ensures every
// input points to an output on a graph-reachable predecessor.
func ValidateWorkflowArtifactGraph(workflow WorkflowTemplate) []WorkflowValidationIssue {
	issues := make([]WorkflowValidationIssue, 0)
	nodes := make(map[int]WorkflowNode, len(workflow.Nodes))
	outputs := make(map[int]map[string]WorkflowArtifactDeclaration, len(workflow.Nodes))
	adjacency := make(map[int][]int, len(workflow.Nodes))
	for _, edge := range workflow.Edges {
		adjacency[edge.SourceNodeID] = append(adjacency[edge.SourceNodeID], edge.DestinationNodeID)
	}
	for index, node := range workflow.Nodes {
		nodes[node.ID] = node
		path := fmt.Sprintf("nodes[%d]", index)
		if node.EffectiveKind() != WorkflowNodeTaskKind && (len(node.ArtifactOutputs) != 0 || len(node.ArtifactInputs) != 0) {
			issues = append(issues, workflowArtifactIssue(
				"WORKFLOW_ARTIFACT_TASK_REQUIRED", "Workflow artifacts require a task node.", path, node.ID,
			))
		}
		if len(node.ArtifactOutputs) > MaxWorkflowArtifactsPerNode {
			issues = append(issues, workflowArtifactIssue(
				"WORKFLOW_ARTIFACT_OUTPUT_LIMIT", fmt.Sprintf("A workflow node can declare at most %d outputs.", MaxWorkflowArtifactsPerNode), path+".artifact_outputs", node.ID,
			))
		}
		declared := make(map[string]WorkflowArtifactDeclaration, len(node.ArtifactOutputs))
		for outputIndex, declaration := range node.ArtifactOutputs {
			outputPath := fmt.Sprintf("%s.artifact_outputs[%d]", path, outputIndex)
			if err := declaration.Validate(); err != nil {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_OUTPUT_INVALID", err.Error(), outputPath, node.ID,
				))
			}
			if _, duplicate := declared[declaration.Name]; duplicate {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_OUTPUT_DUPLICATE", "Workflow artifact output names must be unique per node.", outputPath+".name", node.ID,
				))
			}
			declared[declaration.Name] = declaration
		}
		outputs[node.ID] = declared
	}
	for index, node := range workflow.Nodes {
		path := fmt.Sprintf("nodes[%d]", index)
		if len(node.ArtifactInputs) > MaxWorkflowArtifactInputs {
			issues = append(issues, workflowArtifactIssue(
				"WORKFLOW_ARTIFACT_INPUT_LIMIT", fmt.Sprintf("A workflow node can reference at most %d artifact inputs.", MaxWorkflowArtifactInputs), path+".artifact_inputs", node.ID,
			))
		}
		inputNames := make(map[string]struct{}, len(node.ArtifactInputs))
		for inputIndex, reference := range node.ArtifactInputs {
			inputPath := fmt.Sprintf("%s.artifact_inputs[%d]", path, inputIndex)
			if err := reference.Validate(); err != nil {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_INPUT_INVALID", err.Error(), inputPath, node.ID,
				))
			}
			if _, duplicate := inputNames[reference.Name]; duplicate {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_INPUT_DUPLICATE", "Workflow artifact input names must be unique per node.", inputPath+".name", node.ID,
				))
			}
			inputNames[reference.Name] = struct{}{}
			if _, exists := nodes[reference.SourceNodeID]; !exists {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_SOURCE_MISSING", "Workflow artifact source node does not exist.", inputPath+".source_node_id", node.ID,
				))
				continue
			}
			if _, exists := outputs[reference.SourceNodeID][reference.Output]; !exists {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_OUTPUT_MISSING", "Workflow artifact output is not declared by the source node.", inputPath+".output", node.ID,
				))
			}
			if reference.SourceNodeID == node.ID || !workflowNodeReachable(adjacency, reference.SourceNodeID, node.ID) {
				issues = append(issues, workflowArtifactIssue(
					"WORKFLOW_ARTIFACT_SOURCE_UNREACHABLE", "Workflow artifact source must be a graph-reachable predecessor.", inputPath+".source_node_id", node.ID,
				))
			}
		}
	}
	return issues
}

func workflowNodeReachable(adjacency map[int][]int, sourceID int, destinationID int) bool {
	visited := map[int]struct{}{sourceID: {}}
	queue := []int{sourceID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[current] {
			if next == destinationID {
				return true
			}
			if _, seen := visited[next]; seen {
				continue
			}
			visited[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return false
}

func workflowArtifactIssue(code string, message string, path string, nodeID int) WorkflowValidationIssue {
	return WorkflowValidationIssue{Code: code, Message: message, Path: path, NodeID: &nodeID}
}

func validateWorkflowArtifactJSON(schema WorkflowArtifactSchema, value any, path string, depth int) error {
	if depth > maxWorkflowArtifactSchemaDepth {
		return fmt.Errorf("workflow artifact value at %s exceeds maximum depth", path)
	}
	switch schema.Type {
	case WorkflowArtifactString:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("workflow artifact value at %s must be a string", path)
		}
	case WorkflowArtifactInteger:
		number, ok := value.(json.Number)
		if !ok || strings.ContainsAny(number.String(), ".eE") {
			return fmt.Errorf("workflow artifact value at %s must be an integer", path)
		}
		if _, err := strconv.ParseInt(number.String(), 10, 64); err != nil {
			return fmt.Errorf("workflow artifact value at %s must be a 64-bit integer", path)
		}
	case WorkflowArtifactNumber:
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("workflow artifact value at %s must be a number", path)
		}
		if _, err := strconv.ParseFloat(number.String(), 64); err != nil {
			return fmt.Errorf("workflow artifact value at %s must be a finite number", path)
		}
	case WorkflowArtifactBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("workflow artifact value at %s must be a boolean", path)
		}
	case WorkflowArtifactObject:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("workflow artifact value at %s must be an object", path)
		}
		for _, name := range sortedWorkflowArtifactKeys(object) {
			if _, declared := schema.Properties[name]; !declared {
				return fmt.Errorf("workflow artifact value at %s contains undeclared property %q", path, name)
			}
		}
		for _, name := range schema.Required {
			if _, present := object[name]; !present {
				return fmt.Errorf("workflow artifact value at %s is missing required property %q", path, name)
			}
		}
		for _, name := range sortedWorkflowArtifactKeys(schema.Properties) {
			property := schema.Properties[name]
			propertyValue, present := object[name]
			if !present {
				continue
			}
			if err := validateWorkflowArtifactJSON(property, propertyValue, path+"."+name, depth+1); err != nil {
				return err
			}
		}
	case WorkflowArtifactArray:
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("workflow artifact value at %s must be an array", path)
		}
		for index, item := range items {
			if err := validateWorkflowArtifactJSON(*schema.Items, item, fmt.Sprintf("%s[%d]", path, index), depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("workflow artifact schema type is invalid")
	}
	return nil
}

func sortedWorkflowArtifactKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// WorkflowArtifactReferenceFingerprint identifies provenance without hashing
// or otherwise deriving data from the artifact value.
func WorkflowArtifactReferenceFingerprint(
	runID int,
	sourceNodeID int,
	taskID int,
	attempt int,
	reference WorkflowArtifactReference,
) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "semaphore-workflow-artifact-reference:v1\x00%d\x00%d\x00%d\x00%d\x00%s\x00%d\x00%s\x00%t",
		runID, sourceNodeID, taskID, attempt, reference.Name, reference.SourceNodeID, reference.Output, reference.Required)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}
