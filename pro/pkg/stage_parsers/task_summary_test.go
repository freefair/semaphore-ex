package stage_parsers

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTaskSummaryEvent_Fixture(t *testing.T) {
	file, err := os.Open("testdata/task-summary-v1.jsonl")
	require.NoError(t, err)
	defer file.Close() //nolint:errcheck

	var events []db.TaskSummaryEvent
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		event, recognized, parseErr := ParseTaskSummaryEvent(scanner.Text())
		require.NoError(t, parseErr)
		require.True(t, recognized)
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	require.Len(t, events, 4)
	assert.Equal(t, "web-01.example.com", events[0].Host)
	assert.Equal(t, "Install package", events[0].Stage)
	assert.Equal(t, db.TaskSummaryEventComplete, events[3].Kind)
	assert.Equal(t, 1, events[3].ExpectedHosts)
	assert.NotContains(t, events[1].Error, "super-secret")
	assert.NotContains(t, events[1].Error, "hunter2")
}

func TestParseTaskSummaryEvent_IgnoresOrdinaryOutputAndANSI(t *testing.T) {
	_, recognized, err := ParseTaskSummaryEvent("PLAY [all]")
	require.NoError(t, err)
	assert.False(t, recognized)
	_, recognized, err = ParseTaskSummaryEvent(`ok: [web] => {"msg":"SEMAPHORE_TASK_RESULT {\"version\":1}"}`)
	require.NoError(t, err)
	assert.False(t, recognized)

	event, recognized, err := ParseTaskSummaryEvent("\x1b[32mSEMAPHORE_TASK_RESULT {\"version\":1,\"event\":\"host_summary\",\"event_id\":\"host:WEB\",\"host\":\" WEB. \",\"status\":\"success\"}\x1b[0m")
	require.NoError(t, err)
	assert.True(t, recognized)
	assert.Equal(t, "web", event.Host)
}

func TestParseTaskSummaryEvent_UnsupportedVersionIsRecognized(t *testing.T) {
	event, recognized, err := ParseTaskSummaryEvent(`SEMAPHORE_TASK_RESULT {"version":2,"event":"run_complete","event_id":"done"}`)
	require.NoError(t, err)
	assert.True(t, recognized)
	assert.Equal(t, 2, event.Version)
}

func TestRedactTaskSummaryError_BoundsAndRedacts(t *testing.T) {
	input := `password="hunter2" token=abc Bearer xyz https://user:pass@example.com ` + strings.Repeat("x", 5000)
	redacted := RedactTaskSummaryError(input)
	assert.NotContains(t, redacted, "hunter2")
	assert.NotContains(t, redacted, "abc")
	assert.NotContains(t, redacted, "xyz")
	assert.NotContains(t, redacted, "user:pass")
	assert.LessOrEqual(t, len(redacted), maxSummaryErrorBytes)
}

func TestTaskSummaryCallbackEnvironment_WritesStablePrivatePlugin(t *testing.T) {
	directory := t.TempDir()
	environment, err := TaskSummaryCallbackEnvironment(directory, []string{
		"KEEP=value",
		"ANSIBLE_CALLBACK_PLUGINS=/existing/plugins",
		"ANSIBLE_CALLBACKS_ENABLED=timer",
	})
	require.NoError(t, err)
	assert.Contains(t, environment, "KEEP=value")
	assert.Contains(t, environment, "ANSIBLE_CALLBACK_PLUGINS=/existing/plugins"+string(os.PathListSeparator)+directory)
	assert.Contains(t, environment, "ANSIBLE_CALLBACKS_ENABLED=timer,semaphore_task_summary")

	info, err := os.Stat(directory + "/semaphore_task_summary.py")
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestTaskSummaryCollectionFailureOutput_IsPersistableAndRedacted(t *testing.T) {
	line := TaskSummaryCollectionFailureOutput(errors.New("token=super-secret callback setup failed"))
	event, recognized, err := ParseTaskSummaryEvent(line)
	require.NoError(t, err)
	require.True(t, recognized)
	assert.Equal(t, db.TaskSummaryEventFailure, event.Kind)
	assert.NotContains(t, event.Error, "super-secret")
	assert.Contains(t, event.Error, "callback setup failed")
}

func TestParseTaskSummaryEvent_PreservesBoundedWorkflowOutputs(t *testing.T) {
	event, recognized, err := ParseTaskSummaryEvent(
		`SEMAPHORE_TASK_RESULT {"version":1,"event":"workflow_outputs","event_id":"run:outputs","outputs":{"release":"r1","count":2}}`,
	)
	require.NoError(t, err)
	assert.True(t, recognized)
	assert.Equal(t, db.TaskSummaryEventWorkflowOutputs, event.Kind)
	assert.JSONEq(t, `"r1"`, string(event.Outputs["release"]))
	assert.JSONEq(t, `2`, string(event.Outputs["count"]))
}

func TestParseTaskSummaryEvent_RejectsOversizedWorkflowOutputWithoutEchoingValue(t *testing.T) {
	secretValue := strings.Repeat("secret-value", 7000)
	payload, err := json.Marshal(db.TaskSummaryEvent{
		Version: db.TaskSummarySchemaVersion, Kind: db.TaskSummaryEventWorkflowOutputs,
		EventID: "run:outputs", Outputs: map[string]json.RawMessage{"release": json.RawMessage(`"` + secretValue + `"`)},
	})
	require.NoError(t, err)
	_, recognized, err := ParseTaskSummaryEvent(taskSummaryLinePrefix + string(payload))
	require.Error(t, err)
	assert.True(t, recognized)
	assert.NotContains(t, err.Error(), "secret-value")
}

func TestTaskSummaryCallback_ExtractsOnlyExplicitWorkflowOutputStats(t *testing.T) {
	assert.Contains(t, ansibleTaskSummaryCallback, `run_stats.get("semaphore_workflow_outputs")`)
	assert.Contains(t, ansibleTaskSummaryCallback, `"event": "workflow_outputs"`)
}
