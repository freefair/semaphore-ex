package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	workflowDB "github.com/semaphoreui/semaphore/pro/db"
)

const maxWorkflowEdgeInputBytes = 64 * 1024

type workflowEdgeInputCandidate struct {
	value        json.RawMessage
	edgeID       int
	explicit     bool
	sourceNodeID int
}

// resolveWorkflowEdgeInputs resolves only the public upstream outputs document.
// Declared EX artifact aliases are deliberately excluded: they may be encrypted.
func (s *workflowService) resolveWorkflowEdgeInputs(run db.WorkflowRun, node db.WorkflowRunNode, definition db.WorkflowNode, task *db.Task) (bool, string, []string, error) {
	if definition.CrossProjectTemplateReference != nil {
		return false, "", nil, nil
	}
	plain, err := decodeWorkflowArtifactObject(task.Environment)
	if err != nil {
		return false, "", nil, fmt.Errorf("decode workflow edge input environment: %w", err)
	}
	secret, err := decodeWorkflowArtifactObject(task.Secret)
	if err != nil {
		return false, "", nil, fmt.Errorf("decode workflow edge input secrets: %w", err)
	}
	declared := make(map[string]struct{}, len(definition.ArtifactInputs))
	for _, input := range definition.ArtifactInputs {
		declared[input.Name] = struct{}{}
	}
	survey := make(map[string]db.SurveyVar, len(node.TemplateSnapshot.SurveyVars))
	for _, variable := range node.TemplateSnapshot.SurveyVars {
		survey[variable.Name] = variable
	}
	candidates := map[string]workflowEdgeInputCandidate{}
	edges := make([]db.WorkflowEdge, 0)
	for _, edge := range run.DefinitionSnapshot.Edges {
		if edge.DestinationNodeID == node.WorkflowNodeID {
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(i, j int) bool { return workflowEdgeCanonicalLess(run.DefinitionSnapshot, edges[i], edges[j]) })
	for rank, edge := range edges {
		sourceNode, sourceErr := workflowRunNode(run, edge.SourceNodeID)
		if sourceErr != nil {
			return false, "", nil, sourceErr
		}
		matched, matchErr := workflowDB.EvaluateWorkflowCondition(edge.ConditionProgram, sourceNode.Result)
		if matchErr != nil {
			return false, "", nil, matchErr
		}
		if !matched {
			continue
		}
		values, err := s.workflowEdgeSourceValues(run, edge.SourceNodeID, map[int]bool{})
		if err != nil {
			return false, "", nil, err
		}
		if len(values) == 0 {
			continue
		}
		if edge.EffectiveInputMode() == db.WorkflowEdgeInputExplicit {
			for _, mapping := range edge.InputMappings {
				s.addWorkflowEdgeCandidate(candidates, mapping.Var, values[mapping.Key], edge, rank, true)
			}
			continue
		}
		for name, value := range values {
			if name == "semaphore_workflow_outputs" {
				continue
			}
			if variable, ok := survey[name]; ok && variable.Type != db.SurveyVarSecret {
				s.addWorkflowEdgeCandidate(candidates, name, value, edge, rank, false)
			}
		}
	}
	lines := make([]string, 0)
	total := 0
	for name, variable := range survey {
		if variable.Type == db.SurveyVarSecret {
			continue
		}
		if _, protected := declared[name]; protected {
			continue
		}
		// Task secrets and global credential bindings are injected after the
		// public environment. Do not write or log a public edge/default value
		// for a name that they already own.
		if _, exists := secret[name]; exists {
			continue
		}
		if _, exists := task.GlobalCredentialBindings[name]; exists {
			continue
		}
		staticValue := plain[name]
		value, source := staticValue, "static value"
		fromEdge := false
		if candidate, exists := candidates[name]; exists {
			value, source = candidate.value, fmt.Sprintf("output of node %d", candidate.sourceNodeID)
			fromEdge = true
		}
		coerced, present := coerceWorkflowSurveyInput(variable, value)
		if !present && fromEdge {
			coerced, present = coerceWorkflowSurveyInput(variable, staticValue)
			source = "static value"
			fromEdge = false
		}
		if !present && variable.DefaultValue != nil {
			coerced, present = workflowSurveyDefaultInput(variable)
			source = "survey default"
			fromEdge = false
		}
		if !present {
			if variable.Required {
				return true, fmt.Sprintf("Workflow input %q is required but no valid value is available.", name), nil, nil
			}
			delete(plain, name)
			continue
		}
		if fromEdge {
			total += len(coerced)
			if total > maxWorkflowEdgeInputBytes {
				return true, "Workflow input values exceed the 64 KB limit.", nil, nil
			}
		}
		plain[name] = coerced
		lines = append(lines, fmt.Sprintf("Workflow input %q came from %s.", name, source))
	}
	task.Environment, err = encodeWorkflowArtifactObject(plain)
	return false, "", lines, err
}

func (s *workflowService) addWorkflowEdgeCandidate(candidates map[string]workflowEdgeInputCandidate, name string, value json.RawMessage, edge db.WorkflowEdge, rank int, explicit bool) {
	if len(value) == 0 {
		return
	}
	current, exists := candidates[name]
	if !exists || explicit && !current.explicit || explicit == current.explicit && rank < current.edgeID {
		candidates[name] = workflowEdgeInputCandidate{value: append(json.RawMessage(nil), value...), edgeID: rank, explicit: explicit, sourceNodeID: edge.SourceNodeID}
	}
}

func (s *workflowService) workflowEdgeSourceValues(run db.WorkflowRun, nodeID int, seen map[int]bool) (map[string]json.RawMessage, error) {
	if seen[nodeID] {
		return nil, nil
	}
	seen[nodeID] = true
	definition, err := workflowDefinitionNode(run.DefinitionSnapshot, nodeID)
	if err != nil {
		return nil, err
	}
	node, err := workflowRunNode(run, nodeID)
	if err != nil || node.Status != db.WorkflowRunNodeSucceeded {
		return map[string]json.RawMessage{}, err
	}
	if definition.EffectiveKind() == db.WorkflowNodeTaskKind {
		task, err := s.repository.GetWorkflowRunNodeTask(run.ProjectID, run.ID, nodeID)
		if err != nil || task.Artifacts == nil {
			return map[string]json.RawMessage{}, err
		}
		if node.TaskID == nil || task.ID != *node.TaskID || task.Status != task_logger.TaskSuccessStatus {
			return map[string]json.RawMessage{}, nil
		}
		var document struct {
			Values map[string]json.RawMessage `json:"values"`
		}
		if err = json.Unmarshal([]byte(*task.Artifacts), &document); err != nil {
			return nil, fmt.Errorf("decode workflow task outputs: %w", err)
		}
		// This EX-internal callback payload may contain sensitive declared
		// artifacts. It is never an upstream workflow output, even for an
		// explicit mapping or after an approval/delay pass-through.
		delete(document.Values, "semaphore_workflow_outputs")
		return document.Values, nil
	}
	result := map[string]json.RawMessage{}
	edges := make([]db.WorkflowEdge, 0)
	for _, edge := range run.DefinitionSnapshot.Edges {
		if edge.DestinationNodeID == nodeID {
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(i, j int) bool { return workflowEdgeCanonicalLess(run.DefinitionSnapshot, edges[i], edges[j]) })
	for _, edge := range edges {
		source, err := workflowRunNode(run, edge.SourceNodeID)
		if err != nil {
			return nil, err
		}
		matched, err := workflowDB.EvaluateWorkflowCondition(edge.ConditionProgram, source.Result)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		values, err := s.workflowEdgeSourceValues(run, edge.SourceNodeID, seen)
		if err != nil {
			return nil, err
		}
		for name, value := range values {
			if _, exists := result[name]; !exists {
				result[name] = value
			}
		}
	}
	return result, nil
}

func coerceWorkflowSurveyInput(variable db.SurveyVar, raw json.RawMessage) (json.RawMessage, bool) {
	raw = bytes.TrimSpace(raw)
	if variable.Type == db.SurveyVarSecret || len(raw) == 0 || bytes.Equal(raw, []byte("null")) || !json.Valid(raw) {
		return nil, false
	}
	scalar := func(value json.RawMessage) (string, bool) {
		var decoded any
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		if decoder.Decode(&decoded) != nil {
			return "", false
		}
		switch typed := decoded.(type) {
		case string:
			return typed, true
		case json.Number:
			return typed.String(), true
		case bool:
			if typed {
				return "true", true
			}
			return "false", true
		default:
			return "", false
		}
	}
	if variable.Type == db.SurveyVarSelect {
		var entries []json.RawMessage
		if raw[0] == '[' {
			if json.Unmarshal(raw, &entries) != nil {
				return nil, false
			}
		} else {
			entries = []json.RawMessage{raw}
		}
		if len(entries) == 0 && variable.Required {
			return nil, false
		}
		values := make([]string, 0, len(entries))
		for _, entry := range entries {
			text, ok := scalar(entry)
			if !ok || !workflowSurveyOption(variable, text) {
				return nil, false
			}
			values = append(values, text)
		}
		encoded, _ := json.Marshal(values)
		return encoded, true
	}
	text, scalarValue := scalar(raw)
	switch variable.Type {
	case db.SurveyVarInt:
		if !scalarValue || !isWorkflowIntegerInput(text) {
			return nil, false
		}
	case db.SurveyVarEnum:
		if !scalarValue || !workflowSurveyOption(variable, text) {
			return nil, false
		}
	case db.SurveyVarStr, db.SurveyVarText:
		if !scalarValue {
			var compact bytes.Buffer
			if json.Compact(&compact, raw) != nil {
				return nil, false
			}
			text = compact.String()
		}
	default:
		return nil, false
	}
	if text == "" && variable.Required {
		return nil, false
	}
	encoded, _ := json.Marshal(text)
	return encoded, true
}

func isWorkflowIntegerInput(value string) bool {
	if value == "" {
		return false
	}
	if value[0] == '-' {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func workflowSurveyDefaultInput(variable db.SurveyVar) (json.RawMessage, bool) {
	if variable.Type == db.SurveyVarSelect {
		encoded, _ := json.Marshal(variable.DefaultValue.Values)
		return coerceWorkflowSurveyInput(variable, encoded)
	}
	encoded, _ := json.Marshal(variable.DefaultValue.String())
	return coerceWorkflowSurveyInput(variable, encoded)
}

func workflowSurveyOption(variable db.SurveyVar, value string) bool {
	for _, option := range variable.Values {
		if option.Value == value {
			return true
		}
	}
	return false
}

func workflowEdgeCanonicalLess(workflow db.WorkflowTemplate, left, right db.WorkflowEdge) bool {
	ordinal := func(id int) int {
		for index, node := range workflow.Nodes {
			if node.ID == id {
				return index + 1
			}
		}
		return len(workflow.Nodes) + 1
	}
	if a, b := ordinal(left.SourceNodeID), ordinal(right.SourceNodeID); a != b {
		return a < b
	}
	if a, b := ordinal(left.DestinationNodeID), ordinal(right.DestinationNodeID); a != b {
		return a < b
	}
	if left.Condition != right.Condition {
		return left.Condition < right.Condition
	}
	if left.Expression != right.Expression {
		return left.Expression < right.Expression
	}
	return left.Label < right.Label
}
