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

func TestSupportedRootWorkspaceSelectsFullProductModule(t *testing.T) {
	repositoryRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	workspaceFile := filepath.Join(repositoryRoot, "go.work")
	require.FileExists(t, workspaceFile)

	listCommand := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/semaphoreui/semaphore/pro")
	listCommand.Dir = repositoryRoot
	listCommand.Env = append(os.Environ(), "GOWORK="+workspaceFile)
	selectedModule, err := listCommand.CombinedOutput()
	require.NoError(t, err, string(selectedModule))
	assert.Equal(
		t,
		filepath.Join(repositoryRoot, "test", "edition-contract", "enhanced"),
		strings.TrimSpace(string(selectedModule)),
	)
}

func TestSupportedBuildInputsHaveNoEditionSelector(t *testing.T) {
	repositoryRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	for _, filename := range []string{
		"Taskfile.yml",
		"deployment/docker/server/Dockerfile",
		"deployment/docker/runner/Dockerfile",
		".github/workflows/product_build.yml",
		".github/workflows/product_beta.yml",
		".github/workflows/product_release.yml",
	} {
		content, readErr := os.ReadFile(filepath.Join(repositoryRoot, filename))
		require.NoError(t, readErr, filename)
		assert.NotContains(t, string(content), "APP_BUILD_TYPE", filename)
		assert.NotContains(t, string(content), "VUE_APP_EDITION", filename)
		assert.NotContains(t, string(content), "VUE_APP_BUILD_TYPE", filename)
	}
}

func TestProductWorkflowBuildsEmbeddedFrontendBeforeModuleTests(t *testing.T) {
	repositoryRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(repositoryRoot, ".github", "workflows", "product_build.yml"))
	require.NoError(t, err)

	workflow := string(content)
	frontendBuild := strings.Index(workflow, "task build:fe")
	moduleTests := strings.Index(workflow, "go test ./... -count=1")
	require.NotEqual(t, -1, frontendBuild, "product workflow must build the embedded frontend")
	require.NotEqual(t, -1, moduleTests, "product workflow must run the module tests")
	assert.Less(t, frontendBuild, moduleTests, "embedded frontend must exist before Go compiles api/router.go")
}
