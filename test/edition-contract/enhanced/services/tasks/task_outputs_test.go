package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskOutputsCollectorRejectsReservedStructuredOutput(t *testing.T) {
	collector := NewTaskOutputsCollector()
	require.NotNil(t, collector)

	document, err := collector.Validate(`{"values":{"safe":"value","semaphore_workflow_outputs":{"token":"private"}}}`)

	require.NoError(t, err)
	require.NotNil(t, document)
	assert.JSONEq(t, `{"values":{"safe":"value"}}`, *document)
}

func TestTaskOutputsCollectorUsesPrivateRegularFile(t *testing.T) {
	collector := NewTaskOutputsCollector()
	capture, err := collector.Begin(t.TempDir(), 41)
	require.NoError(t, err)
	defer capture.Close()

	env := capture.Env()
	require.Len(t, env, 1)
	path := env[0][len("SEMAPHORE_OUTPUTS_FILE="):]
	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular())
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.Equal(t, filepath.Dir(path), filepath.Clean(filepath.Dir(path)))
}

func TestTaskOutputsCaptureFileOverridesTerraformAndPreservesJSONNumbers(t *testing.T) {
	collector := NewTaskOutputsCollector()
	capture, err := collector.Begin(t.TempDir(), 41)
	require.NoError(t, err)
	defer capture.Close()
	path := capture.Env()[0][len("SEMAPHORE_OUTPUTS_FILE="):]
	require.NoError(t, os.WriteFile(path, []byte(`{"answer":9007199254740993}`), 0o600))

	document, _, err := capture.Collect([]byte(`{"answer":{"value":1},"other":{"value":true}}`))
	require.NoError(t, err)
	require.NotNil(t, document)
	assert.JSONEq(t, `{"values":{"answer":9007199254740993,"other":true}}`, *document)
}

func TestTaskOutputsCaptureAnsibleCallbackWritesOnlyRunScope(t *testing.T) {
	capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 43)
	require.NoError(t, err)
	defer capture.Close()

	dir, err := capture.AnsibleCallbackDir()
	require.NoError(t, err)
	plugin, err := os.ReadFile(filepath.Join(dir, "semaphore_outputs.py"))
	require.NoError(t, err)
	assert.Contains(t, string(plugin), ".get('_run', {})")
	assert.Contains(t, string(plugin), "isinstance(run_values, dict)")
	assert.Contains(t, string(plugin), "values.pop('semaphore_workflow_outputs', None)")
	assert.NotContains(t, string(plugin), "CALLBACK_NEEDS_ENABLED")
}

func TestTaskOutputsCaptureSkipsSensitiveTerraformOutput(t *testing.T) {
	collector := NewTaskOutputsCollector()
	capture, err := collector.Begin(t.TempDir(), 42)
	require.NoError(t, err)
	defer capture.Close()

	document, _, err := capture.Collect([]byte(`{"token":{"value":"distinctive-secret-value","sensitive":true},"safe":{"value":3}}`))
	require.NoError(t, err)
	require.NotNil(t, document)
	assert.JSONEq(t, `{"values":{"safe":3},"skipped":{"token":"sensitive"}}`, *document)
	assert.NotContains(t, *document, "distinctive-secret-value")
}

func TestTaskOutputsCaptureKeepsSmallPublicTerraformOutputAfterLargeSensitiveValue(t *testing.T) {
	collector := NewTaskOutputsCollector()
	capture, err := collector.Begin(t.TempDir(), 42)
	require.NoError(t, err)
	defer capture.Close()

	sensitiveValue := strings.Repeat("sensitive-marker-", (256*1024)/len("sensitive-marker-")+1)
	terraform := fmt.Sprintf(`{"large_secret":{"value":%q,"sensitive":true},"safe":{"value":"kept","sensitive":false}}`, sensitiveValue)
	document, notes, err := capture.CollectContent(nil, []byte(terraform))

	require.NoError(t, err)
	require.NotNil(t, document)
	assert.JSONEq(t, `{"values":{"safe":"kept"},"skipped":{"large_secret":"sensitive"}}`, *document)
	assert.NotContains(t, *document, "sensitive-marker")
	assert.LessOrEqual(t, len(*document), maxPublicOutputDocumentBytes)
	assert.Contains(t, notes, `Output "large_secret" was not captured: sensitive`)
}

func TestTaskOutputsCaptureRejectsInvalidProcessFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "malformed", content: `{"answer":`},
		{name: "array", content: `["answer"]`},
		{name: "bad name", content: `{"not valid":true}`},
		{name: "too many values", content: outputObject(maxPublicOutputValues + 1)},
		{name: "oversize value", content: `{"answer":"` + strings.Repeat("a", maxPublicOutputValueBytes) + `"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 45)
			require.NoError(t, err)
			defer capture.Close()
			path := capture.Env()[0][len("SEMAPHORE_OUTPUTS_FILE="):]
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))
			document, _, err := capture.Collect(nil)
			assert.Error(t, err)
			assert.Nil(t, document)
		})
	}
}

func TestTaskOutputsCaptureMissingOrEmptyFileHasNoOutput(t *testing.T) {
	collector := NewTaskOutputsCollector()
	capture, err := collector.Begin(t.TempDir(), 46)
	require.NoError(t, err)
	defer capture.Close()
	path := capture.Env()[0][len("SEMAPHORE_OUTPUTS_FILE="):]
	require.NoError(t, os.Remove(path))
	document, _, err := capture.Collect(nil)
	require.NoError(t, err)
	assert.Nil(t, document)

	capture, err = collector.Begin(t.TempDir(), 47)
	require.NoError(t, err)
	defer capture.Close()
	document, _, err = capture.Collect(nil)
	require.NoError(t, err)
	assert.Nil(t, document)
}

func TestTaskOutputsCaptureRejectsSymlinkAndFIFO(t *testing.T) {
	capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 48)
	require.NoError(t, err)
	defer capture.Close()
	path := capture.Env()[0][len("SEMAPHORE_OUTPUTS_FILE="):]
	target := filepath.Join(t.TempDir(), "target.json")
	require.NoError(t, os.WriteFile(target, []byte(`{"answer":true}`), 0o600))
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(target, path))
	document, _, err := capture.Collect(nil)
	assert.Error(t, err)
	assert.Nil(t, document)
}

func TestTaskOutputsCollectorCanonicalizesRemoteDocuments(t *testing.T) {
	collector := NewTaskOutputsCollector()
	document, err := collector.Validate(`{"values":{"id":9007199254740993,"nested":{"semaphore_workflow_outputs":"drop","keep":true}},"skipped":{"token":"sensitive","semaphore_workflow_outputs":"limit"}}`)
	require.NoError(t, err)
	require.NotNil(t, document)
	assert.JSONEq(t, `{"values":{"id":9007199254740993,"nested":{"keep":true}},"skipped":{"token":"sensitive"}}`, *document)

	for _, invalid := range []string{
		`{"values":{"safe":true},"extra":true}`,
		`{"values":{"safe":true},"skipped":{"token":"unknown"}}`,
		`{"values":{"safe":true},"skipped":{"not valid":"limit"}}`,
	} {
		document, err = collector.Validate(invalid)
		assert.Error(t, err)
		assert.Nil(t, document)
	}
}

func TestTaskOutputsCaptureTerraformSkipsAreDeterministicAndSecretFree(t *testing.T) {
	capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 49)
	require.NoError(t, err)
	defer capture.Close()

	terraform := `{"z":{"value":1},"bad name":{"value":"secret-value"},"a":{"value":2},"token":{"value":"secret-value","sensitive":true}}`
	document, notes, err := capture.Collect([]byte(terraform))
	require.NoError(t, err)
	require.NotNil(t, document)
	assert.JSONEq(t, `{"values":{"a":2,"z":1},"skipped":{"bad name":"invalid_name","token":"sensitive"}}`, *document)
	assert.Equal(t, []string{
		`Output "bad name" was not captured: invalid_name`,
		`Output "token" was not captured: sensitive`,
	}, notes)
	assert.NotContains(t, strings.Join(notes, "\n"), "secret-value")
}

func TestTaskOutputsCaptureTerraformCountLimitSkipsRatherThanFails(t *testing.T) {
	entries := make([]string, 0, maxPublicOutputValues+1)
	for index := 0; index <= maxPublicOutputValues; index++ {
		entries = append(entries, fmt.Sprintf(`"value_%03d":{"value":%d}`, index, index))
	}
	capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 50)
	require.NoError(t, err)
	defer capture.Close()
	document, notes, err := capture.Collect([]byte("{" + strings.Join(entries, ",") + "}"))
	require.NoError(t, err)
	require.NotNil(t, document)
	assert.Contains(t, *document, `"value_100":"limit"`)
	assert.Equal(t, []string{`Output "value_100" was not captured: limit`}, notes)
}

func TestTaskOutputsCaptureTerraformDocumentLimitSkipsRatherThanFails(t *testing.T) {
	entries := make([]string, 0, 10)
	value := strings.Repeat("a", maxPublicOutputValueBytes-32)
	for index := 0; index < 10; index++ {
		entries = append(entries, fmt.Sprintf("\"value_%03d\":{\"value\":\"%s\"}", index, value))
	}
	capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 52)
	require.NoError(t, err)
	defer capture.Close()
	document, notes, err := capture.Collect([]byte("{" + strings.Join(entries, ",") + "}"))
	require.NoError(t, err)
	require.NotNil(t, document)
	assert.LessOrEqual(t, len(*document), maxPublicOutputDocumentBytes)
	assert.Contains(t, *document, "\"value_009\":\"limit\"")
	assert.NotEmpty(t, notes)
}

func outputObject(count int) string {
	entries := make([]string, 0, count)
	for index := 0; index < count; index++ {
		entries = append(entries, fmt.Sprintf(`"value_%d":true`, index))
	}
	return "{" + strings.Join(entries, ",") + "}"
}
