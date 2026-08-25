package pro_interfaces_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceSelectsCleanRoomEnhancedModule(t *testing.T) {
	repositoryRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	workspaceRoot := filepath.Join(repositoryRoot, "test", "edition-contract")
	workspaceFile := filepath.Join(workspaceRoot, "go.work")

	listCommand := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/semaphoreui/semaphore/pro")
	listCommand.Dir = workspaceRoot
	listCommand.Env = append(os.Environ(), "GOWORK="+workspaceFile)
	selectedModule, err := listCommand.CombinedOutput()
	require.NoError(t, err, string(selectedModule))
	assert.Equal(t, filepath.Join(workspaceRoot, "enhanced"), strings.TrimSpace(string(selectedModule)))

	runCommand := exec.Command("go", "run", "-mod=readonly", "./consumer")
	runCommand.Dir = workspaceRoot
	runCommand.Env = append(os.Environ(), "GOWORK="+workspaceFile)
	output, err := runCommand.CombinedOutput()
	require.NoError(t, err, string(output))
	assert.Equal(t, "clean-room-test-1", string(output))

	buildCommand := exec.Command("go", "build", "-mod=readonly", "-o", filepath.Join(t.TempDir(), "semaphore"), "github.com/semaphoreui/semaphore/cli")
	buildCommand.Dir = workspaceRoot
	buildCommand.Env = append(os.Environ(), "GOWORK="+workspaceFile)
	buildOutput, err := buildCommand.CombinedOutput()
	require.NoError(t, err, string(buildOutput))
}
