package tasks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pro_interfaces"
)

const reservedStructuredWorkflowOutputs = "semaphore_workflow_outputs"

const (
	maxPublicOutputDocumentBytes = 256 * 1024
	maxPublicOutputValues        = 100
	maxPublicOutputValueBytes    = 32 * 1024
)

type taskOutputsCollector struct{}

// NewTaskOutputsCollector supplies the full-product implementation selected by
// the workspace. The Community replacement remains deliberately unwired.
func NewTaskOutputsCollector() pro_interfaces.TaskOutputsCollector { return taskOutputsCollector{} }

func (taskOutputsCollector) Begin(dir string, taskID int) (pro_interfaces.TaskOutputsCapture, error) {
	if taskID < 1 {
		return nil, fmt.Errorf("task outputs require a task id")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	privateDir, err := os.MkdirTemp(dir, fmt.Sprintf("task_%d_outputs_", taskID))
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(privateDir, 0o700); err != nil {
		_ = os.RemoveAll(privateDir)
		return nil, err
	}
	path := filepath.Join(privateDir, "outputs.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		err = file.Close()
	}
	if err != nil {
		_ = os.RemoveAll(privateDir)
		return nil, err
	}
	return &taskOutputsCapture{path: path, dir: privateDir}, nil
}

func (taskOutputsCollector) Validate(document string) (*string, error) {
	if strings.TrimSpace(document) == "" {
		return nil, nil
	}
	if len(document) > maxPublicOutputDocumentBytes {
		return nil, fmt.Errorf("outputs document exceeds %d bytes", maxPublicOutputDocumentBytes)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(document), &raw); err != nil {
		return nil, fmt.Errorf("outputs must be a JSON object")
	}
	if len(raw) > 2 || raw["values"] == nil {
		return nil, fmt.Errorf("outputs must contain only values and skipped")
	}
	for field := range raw {
		if field != "values" && field != "skipped" {
			return nil, fmt.Errorf("outputs must contain only values and skipped")
		}
	}
	values := make(map[string]json.RawMessage)
	if err := json.Unmarshal(raw["values"], &values); err != nil || values == nil {
		return nil, fmt.Errorf("outputs must contain a values object")
	}
	skipped := make(map[string]string)
	if skippedRaw := raw["skipped"]; skippedRaw != nil {
		if err := json.Unmarshal(skippedRaw, &skipped); err != nil || skipped == nil {
			return nil, fmt.Errorf("outputs skipped must be an object")
		}
	}
	delete(values, reservedStructuredWorkflowOutputs)
	delete(skipped, reservedStructuredWorkflowOutputs)
	if len(values) > maxPublicOutputValues {
		return nil, fmt.Errorf("outputs contain more than %d values", maxPublicOutputValues)
	}
	for name, value := range values {
		if !db.IsValidWorkflowOutputName(name) {
			return nil, fmt.Errorf("outputs contain an invalid name")
		}
		compact, err := compactOutputValue(value)
		if err != nil {
			return nil, fmt.Errorf("outputs contain an invalid value")
		}
		if len(compact) > maxPublicOutputValueBytes {
			return nil, fmt.Errorf("outputs contain a value exceeding %d bytes", maxPublicOutputValueBytes)
		}
		values[name] = removeReservedOutputKeys(compact)
	}
	for name, reason := range skipped {
		// Terraform must record an unusable source name as invalid_name so the
		// workflow editor can explain why it cannot be mapped. Other reasons
		// must always refer to a valid output key.
		if !db.IsValidWorkflowOutputName(name) && reason != "invalid_name" {
			return nil, fmt.Errorf("outputs contain an invalid skipped name")
		}
		if !isAllowedOutputSkipReason(reason) {
			return nil, fmt.Errorf("outputs contain an invalid skipped reason")
		}
	}
	if len(values) == 0 && len(skipped) == 0 {
		return nil, nil
	}
	canonical, err := encodeOutputDocument(values, skipped)
	if err != nil {
		return nil, err
	}
	if len(canonical) > maxPublicOutputDocumentBytes {
		return nil, fmt.Errorf("outputs document exceeds %d bytes", maxPublicOutputDocumentBytes)
	}
	result := string(canonical)
	return &result, nil
}

func isAllowedOutputSkipReason(reason string) bool {
	switch reason {
	case "sensitive", "too_large", "invalid_name", "limit":
		return true
	default:
		return false
	}
}

type taskOutputsCapture struct {
	path        string
	dir         string
	callbackDir string
}

func (c *taskOutputsCapture) Env() []string { return []string{"SEMAPHORE_OUTPUTS_FILE=" + c.path} }

func (c *taskOutputsCapture) AnsibleCallbackDir() (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(c.path), "semaphore_outputs_callback_")
	if err != nil {
		return "", err
	}
	if err = os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	plugin := `from ansible.plugins.callback import CallbackBase
import json, os
class CallbackModule(CallbackBase):
    CALLBACK_VERSION = 2.0
    CALLBACK_TYPE = 'aggregate'
    CALLBACK_NAME = 'semaphore_outputs'
    def v2_playbook_on_stats(self, stats):
        run_values = (getattr(stats, 'custom', {}) or {}).get('_run', {})
        values = dict(run_values) if isinstance(run_values, dict) else {}
        values.pop('semaphore_workflow_outputs', None)
        with open(os.environ['SEMAPHORE_OUTPUTS_FILE'], 'w', encoding='utf-8') as out:
            json.dump(values, out, separators=(',', ':'))
`
	if err = os.WriteFile(filepath.Join(dir, "semaphore_outputs.py"), []byte(plugin), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	c.callbackDir = dir
	return dir, nil
}

func (c *taskOutputsCapture) Collect(terraformOutputs []byte) (*string, []string, error) {
	content, err := readTaskOutputsFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			content = nil
		} else {
			return nil, nil, err
		}
	}
	return c.CollectContent(content, terraformOutputs)
}

// CollectContent combines private process output retrieved by an isolated
// executor with Terraform output. It deliberately shares the local collector
// path so file overrides, sensitive filtering, limits and canonicalization
// cannot drift between execution strategies.
func (c *taskOutputsCapture) CollectContent(content []byte, terraformOutputs []byte) (*string, []string, error) {
	if len(content) > maxPublicOutputDocumentBytes {
		return nil, nil, fmt.Errorf("outputs document exceeds %d bytes", maxPublicOutputDocumentBytes)
	}
	values, skipped, notes := collectTerraformOutputs(terraformOutputs)
	terraformValueNames := make(map[string]struct{}, len(values))
	for name := range values {
		terraformValueNames[name] = struct{}{}
	}
	fileValues := make(map[string]json.RawMessage)
	if strings.TrimSpace(string(content)) != "" {
		if err := json.Unmarshal(content, &fileValues); err != nil || fileValues == nil {
			return nil, nil, fmt.Errorf("outputs must be a JSON object")
		}
		for name, value := range fileValues {
			values[name] = value
			delete(skipped, name)
		}
	}
	fitTerraformValues(values, skipped, terraformValueNames, fileValues, &notes)
	encoded, err := encodeOutputDocument(values, skipped)
	if err != nil {
		return nil, nil, err
	}
	document, err := taskOutputsCollector{}.Validate(string(encoded))
	return document, notes, err
}

// fitTerraformValues reserves the document budget for process-written values.
// Terraform capture is best effort, so later sorted Terraform names are skipped
// instead of turning an otherwise successful producer into a failed task.
func fitTerraformValues(values map[string]json.RawMessage, skipped map[string]string, terraformValueNames map[string]struct{}, fileValues map[string]json.RawMessage, notes *[]string) {
	names := make([]string, 0, len(terraformValueNames))
	for name := range terraformValueNames {
		if _, overridden := fileValues[name]; !overridden {
			names = append(names, name)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for len(values) > maxPublicOutputValues || outputDocumentTooLarge(values, skipped) {
		if len(names) == 0 {
			return
		}
		name := names[0]
		names = names[1:]
		if _, present := values[name]; !present {
			continue
		}
		delete(values, name)
		skipped[name] = "limit"
		*notes = append(*notes, fmt.Sprintf("Output %q was not captured: limit", name))
	}
}

func outputDocumentTooLarge(values map[string]json.RawMessage, skipped map[string]string) bool {
	document, err := encodeOutputDocument(values, skipped)
	return err != nil || len(document) > maxPublicOutputDocumentBytes
}

func collectTerraformOutputs(terraformOutputs []byte) (map[string]json.RawMessage, map[string]string, []string) {
	values := make(map[string]json.RawMessage)
	skipped := make(map[string]string)
	if len(terraformOutputs) == 0 {
		return values, skipped, nil
	}
	var terraform map[string]struct {
		Value     json.RawMessage `json:"value"`
		Sensitive bool            `json:"sensitive"`
	}
	if err := json.Unmarshal(terraformOutputs, &terraform); err != nil || terraform == nil {
		return values, skipped, []string{"Terraform outputs were not captured: invalid JSON"}
	}
	names := make([]string, 0, len(terraform))
	for name := range terraform {
		names = append(names, name)
	}
	sort.Strings(names)
	notes := make([]string, 0)
	for _, name := range names {
		output := terraform[name]
		reason := ""
		switch {
		case output.Sensitive:
			reason = "sensitive"
		case !db.IsValidWorkflowOutputName(name):
			reason = "invalid_name"
		default:
			compact, err := compactOutputValue(output.Value)
			if err != nil || len(compact) > maxPublicOutputValueBytes {
				reason = "too_large"
			} else if len(values) >= maxPublicOutputValues {
				reason = "limit"
			} else {
				values[name] = compact
			}
		}
		if reason != "" {
			skipped[name] = reason
			notes = append(notes, fmt.Sprintf("Output %q was not captured: %s", name, reason))
		}
	}
	return values, skipped, notes
}

func compactOutputValue(value json.RawMessage) (json.RawMessage, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

// removeReservedOutputKeys walks arbitrary JSON values so neither the legacy
// structured channel nor a nested copy of it can cross the task boundary.
func removeReservedOutputKeys(value json.RawMessage) json.RawMessage {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if decoder.Decode(&decoded) != nil {
		return value
	}
	stripReservedOutputKey(decoded)
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return value
	}
	return encoded
}

func stripReservedOutputKey(value any) {
	switch nested := value.(type) {
	case map[string]any:
		delete(nested, reservedStructuredWorkflowOutputs)
		for _, child := range nested {
			stripReservedOutputKey(child)
		}
	case []any:
		for _, child := range nested {
			stripReservedOutputKey(child)
		}
	}
}

func encodeOutputDocument(values map[string]json.RawMessage, skipped map[string]string) ([]byte, error) {
	return json.Marshal(struct {
		Values  map[string]json.RawMessage `json:"values"`
		Skipped map[string]string          `json:"skipped,omitempty"`
	}{Values: values, Skipped: skipped})
}

func (c *taskOutputsCapture) Close() {
	if c.dir != "" {
		_ = os.RemoveAll(c.dir)
	}
	if c.callbackDir != "" {
		_ = os.RemoveAll(c.callbackDir)
	}
}
