//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tasks

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestTaskOutputsCaptureRejectsFIFOWithoutBlocking(t *testing.T) {
	capture, err := NewTaskOutputsCollector().Begin(t.TempDir(), 51)
	require.NoError(t, err)
	defer capture.Close()
	path := capture.Env()[0][len("SEMAPHORE_OUTPUTS_FILE="):]
	require.NoError(t, os.Remove(path))
	require.NoError(t, unix.Mkfifo(path, 0o600))

	document, _, err := capture.Collect(nil)

	assert.Error(t, err)
	assert.Nil(t, document)
}
