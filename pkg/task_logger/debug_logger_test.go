package task_logger

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/pkg/taskredaction"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func captureDebugOutput(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	prevOut := log.StandardLogger().Out
	prevLevel := log.GetLevel()
	log.SetOutput(&buf)
	log.SetLevel(log.DebugLevel)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetLevel(prevLevel)
	})

	return &buf
}

func TestDebugLogger_Log(t *testing.T) {
	buf := captureDebugOutput(t)

	logger := DebugLogger{Prefix: "browse"}
	logger.Log("hello")
	logger.Logf("value=%d", 42)
	logger.LogWithTime(time.Now(), "with time")

	out := buf.String()
	assert.Contains(t, out, "level=debug")
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "value=42")
	assert.Contains(t, out, "with time")
	assert.Contains(t, out, "prefix=browse")
}

func TestDebugLogger_SilentAboveDebugLevel(t *testing.T) {
	buf := captureDebugOutput(t)
	log.SetLevel(log.InfoLevel)

	DebugLogger{}.Log("hidden")

	assert.Empty(t, buf.String())
}

func TestDebugLogger_LogCmdDiscardsOutputAboveDebugLevel(t *testing.T) {
	buf := captureDebugOutput(t)
	log.SetLevel(log.InfoLevel)

	cmd := exec.Command("sh", "-c", "echo out-line; echo err-line 1>&2")
	finish := DebugLogger{}.LogCmd(cmd)

	require.NoError(t, cmd.Run())
	finish()

	assert.Empty(t, buf.String())
}

func TestDebugLogger_LogCmd(t *testing.T) {
	buf := captureDebugOutput(t)

	cmd := exec.Command("sh", "-c", "echo out-line; echo err-line 1>&2")
	finish := DebugLogger{}.LogCmd(cmd)

	require.NoError(t, cmd.Run())
	finish()
	finish() // idempotent

	out := buf.String()
	assert.Contains(t, out, "out-line")
	assert.Contains(t, out, "err-line")
}

func TestDebugLogger_LogCmdRedactsGitCredential(t *testing.T) {
	buf := captureDebugOutput(t)

	const credential = "browse-password=synthetic"
	cmd := exec.Command("sh", "-c", "echo 'fatal: could not authenticate to https://user:browse-password%3Dsynthetic@git.example/repository.git' 1>&2")
	finish := DebugLogger{Redactor: taskredaction.NewFromTaskSecretAndValues("", nil, []string{credential})}.LogCmd(cmd)

	require.NoError(t, cmd.Run())
	finish()

	assert.NotContains(t, buf.String(), credential)
	assert.NotContains(t, buf.String(), "browse-password%3Dsynthetic")
	assert.Contains(t, buf.String(), "[REDACTED]")
}
