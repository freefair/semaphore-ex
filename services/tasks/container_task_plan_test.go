package tasks

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerTaskPlanPackagesOnlyPreparedTaskMaterial(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })

	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(filepath.Join(repositoryPath, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, "scripts", "run.sh"), []byte("#!/bin/sh\necho ready\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("must not be bundled"), 0o600))

	executor := &LocalExecutor{
		Task: db.Task{ID: 42},
		Template: db.Template{
			ID:        17,
			ProjectID: 9,
			App:       db.AppBash,
			Playbook:  "scripts/run.sh",
		},
		Inventory: db.Inventory{ID: 5, ProjectID: 9, Type: db.InventoryStatic},
		Repository: db.Repository{
			ID:        3,
			ProjectID: 9,
			GitURL:    repositoryPath,
		},
		prepared:        true,
		preparedEnv:     []string{"VISIBLE=value", "SECRET_VALUE=task-secret"},
		preparedArgsMap: map[string][]string{"default": {"scripts/run.sh", "answer=42"}},
	}

	inventoryPath := executor.tmpInventoryFullPath()
	require.NoError(t, os.MkdirAll(filepath.Dir(inventoryPath), 0o755))
	require.NoError(t, os.WriteFile(inventoryPath, []byte("localhost ansible_connection=local\n"), 0o600))

	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, plan.Bundle.Close()) })

	assert.Equal(t, db.AppBash, plan.App)
	assert.Equal(t, []string{"/bin/sh", "/semaphore/bundle/run.sh", "run"}, plan.Command(ContainerTaskStageRun))

	entries := readTaskBundle(t, plan.Bundle)
	assert.Equal(t, "#!/bin/sh\necho ready\n", string(entries["repository/scripts/run.sh"]))
	assert.Equal(t, "localhost ansible_connection=local\n", string(entries["inventory/hosts"]))
	assert.Contains(t, string(entries["credentials/environment.sh"]), "export VISIBLE='value'")
	assert.Contains(t, string(entries["credentials/environment.sh"]), "export SECRET_VALUE='task-secret'")
	assert.Contains(t, string(entries["run.sh"]), "set -eu")
	assert.Contains(t, string(entries["run.sh"]), "'/workspace/scripts/run.sh'")
	assert.NotContains(t, entries, "unrelated.txt")
	for name, contents := range entries {
		assert.NotContains(t, string(contents), root, "bundle entry %s leaked a runner-host path", name)
	}
}

func TestContainerTaskPlanExcludesGitMetadataRecursively(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })

	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(filepath.Join(repositoryPath, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repositoryPath, "vendor", "module", ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, ".git", "config"), []byte("https://user:credential@example.invalid/repo.git"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, "vendor", "module", ".git", "config"), []byte("nested-credential"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, "playbook.yml"), []byte("---\n"), 0o644))

	executor := preparedContainerExecutor(repositoryPath)
	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	bundle, err := io.ReadAll(plan.Bundle)
	require.NoError(t, err)
	require.NoError(t, plan.Bundle.Close())

	assert.NotContains(t, string(bundle), "user:credential")
	assert.NotContains(t, string(bundle), "nested-credential")
	entries := readTaskBundle(t, bytes.NewReader(bundle))
	assert.Contains(t, entries, "repository/playbook.yml")
	for name := range entries {
		assert.NotContains(t, strings.Split(name, "/"), ".git")
	}
}

func TestContainerTaskPlanRejectsLinksAndSpecialFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("link and FIFO fixtures require a Unix filesystem")
	}

	tests := []struct {
		name   string
		create func(t *testing.T, repositoryPath string)
	}{
		{
			name: "relative symlink",
			create: func(t *testing.T, repositoryPath string) {
				require.NoError(t, os.Symlink("target", filepath.Join(repositoryPath, "link")))
			},
		},
		{
			name: "absolute symlink",
			create: func(t *testing.T, repositoryPath string) {
				require.NoError(t, os.Symlink("/etc/passwd", filepath.Join(repositoryPath, "link")))
			},
		},
		{
			name: "hard link",
			create: func(t *testing.T, repositoryPath string) {
				target := filepath.Join(repositoryPath, "target")
				require.NoError(t, os.WriteFile(target, []byte("data"), 0o600))
				require.NoError(t, os.Link(target, filepath.Join(repositoryPath, "hard-link")))
			},
		},
		{
			name: "fifo",
			create: func(t *testing.T, repositoryPath string) {
				require.NoError(t, syscall.Mkfifo(filepath.Join(repositoryPath, "pipe"), 0o600))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			previousConfig := util.Config
			util.Config = &util.ConfigType{TmpPath: root}
			t.Cleanup(func() { util.Config = previousConfig })
			repositoryPath := filepath.Join(root, "repository")
			require.NoError(t, os.MkdirAll(repositoryPath, 0o755))
			test.create(t, repositoryPath)

			executor := preparedContainerExecutor(repositoryPath)
			plan, err := executor.ContainerTaskPlan()
			require.NoError(t, err)
			_, err = io.ReadAll(plan.Bundle)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported")
		})
	}
}

func TestContainerTaskEnvironmentKeepsShellMetacharactersAsData(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repositoryPath, 0o755))

	marker := filepath.Join(root, "injected")
	value := "quote' $(touch " + marker + ")\nsecond line --option"
	executor := preparedContainerExecutor(repositoryPath)
	executor.preparedEnv = []string{"TASK_VALUE=" + value}
	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	entries := readTaskBundle(t, plan.Bundle)
	require.NoError(t, plan.Bundle.Close())

	scriptPath := filepath.Join(root, "environment.sh")
	require.NoError(t, os.WriteFile(scriptPath, entries["credentials/environment.sh"], 0o600))
	command := exec.Command("/bin/sh", "-c", `. "$1"; printf '%s' "$TASK_VALUE"`, "sh", scriptPath)
	actual, err := command.Output()
	require.NoError(t, err)
	assert.Equal(t, value, string(actual))
	_, err = os.Stat(marker)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestContainerTaskBundleKeepsSecretsReadOnlyAndRunnerPathsOut(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repositoryPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, "run.sh"), []byte("#!/bin/sh\necho ready\n"), 0o755))

	executor := preparedContainerExecutor(repositoryPath)
	executor.preparedEnv = []string{
		"SSH_AUTH_SOCK=" + filepath.Join(root, "runner-agent.sock"),
		"SURVEY_SECRET=secret-value",
	}
	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	bundle, err := io.ReadAll(plan.Bundle)
	require.NoError(t, err)
	require.NoError(t, plan.Bundle.Close())

	headers := readTaskBundleHeaders(t, bytes.NewReader(bundle))
	assert.Equal(t, int64(0o700), headers["credentials"].Mode)
	assert.Equal(t, int64(0o600), headers["credentials/environment.sh"].Mode)
	assert.Equal(t, int64(0o500), headers["run.sh"].Mode)
	assert.Equal(t, int64(0o555), headers["repository"].Mode)
	assert.Equal(t, int64(0o555), headers["repository/run.sh"].Mode)

	entries := readTaskBundle(t, bytes.NewReader(bundle))
	assert.Contains(t, string(entries["credentials/environment.sh"]), "SURVEY_SECRET='secret-value'")
	assert.NotContains(t, string(bundle), root)
	assert.NotContains(t, string(bundle), "runner-agent.sock")
	runScriptContents := string(entries["run.sh"])
	assert.Contains(t, runScriptContents, ".semaphore/ssh-agent.sock")
	assert.Contains(t, runScriptContents, "ANSIBLE_REMOTE_TMP=/tmp/.ansible/tmp")
	assert.Greater(t,
		strings.Index(runScriptContents, "ANSIBLE_REMOTE_TMP=/tmp/.ansible/tmp"),
		strings.Index(runScriptContents, "credentials/environment.sh"),
		"the runner-owned Ansible temp path must override task environment values",
	)

	runScript := filepath.Join(root, "container-run.sh")
	require.NoError(t, os.WriteFile(runScript, entries["run.sh"], 0o500))
	command := exec.Command("/bin/sh", "-n", runScript)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}

func TestContainerTaskArchiveRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "."} {
		t.Run(name, func(t *testing.T) {
			var bundle bytes.Buffer
			archive := tar.NewWriter(&bundle)
			err := addContainerFile(archive, name, 0o400, []byte("data"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsafe archive path")
		})
	}
}

func TestContainerTaskEnvironmentRejectsNUL(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repositoryPath, 0o755))

	executor := preparedContainerExecutor(repositoryPath)
	executor.preparedEnv = []string{"TASK_VALUE=before\x00after"}
	_, err := executor.ContainerTaskPlan()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "NUL")
}

func TestContainerTaskPlanDescribesTerraformStages(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(filepath.Join(repositoryPath, "infra"), 0o755))

	executor := &LocalExecutor{
		Task: db.Task{ID: 8},
		Template: db.Template{
			ID:        4,
			ProjectID: 2,
			App:       db.AppTerraform,
			Playbook:  "infra",
		},
		Repository:        db.Repository{ID: 1, ProjectID: 2, GitURL: repositoryPath},
		prepared:          true,
		preparedArgsMap:   map[string][]string{"init": {"-backend-config=address=test"}, "plan": {"-var", "name=value"}, "apply": {"-var", "name=value"}},
		preparedParams:    &db.TerraformTaskParams{Plan: false},
		preparedTplParams: &db.TerraformTemplateParams{AutoApprove: true},
	}

	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, plan.Bundle.Close()) })

	assert.True(t, plan.Terraform.AutoApprove)
	assert.False(t, plan.Terraform.PlanOnly)
	assert.Equal(t, []string{"/bin/sh", "/semaphore/bundle/run.sh", "bootstrap"}, plan.Command(ContainerTaskStageBootstrap))
	assert.Equal(t, []string{"/bin/sh", "/semaphore/bundle/run.sh", "plan"}, plan.Command(ContainerTaskStagePlan))
	assert.Equal(t, []string{"/bin/sh", "/semaphore/bundle/run.sh", "apply"}, plan.Command(ContainerTaskStageApply))
}

func readTaskBundle(t *testing.T, bundle io.Reader) map[string][]byte {
	t.Helper()
	entries := make(map[string][]byte)
	reader := tar.NewReader(bundle)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		var contents bytes.Buffer
		_, err = io.Copy(&contents, reader)
		require.NoError(t, err)
		entries[header.Name] = contents.Bytes()
	}
	return entries
}

func readTaskBundleHeaders(t *testing.T, bundle io.Reader) map[string]*tar.Header {
	t.Helper()
	headers := make(map[string]*tar.Header)
	reader := tar.NewReader(bundle)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		copy := *header
		headers[header.Name] = &copy
	}
	return headers
}

func preparedContainerExecutor(repositoryPath string) *LocalExecutor {
	return &LocalExecutor{
		Task:       db.Task{ID: 1},
		Template:   db.Template{ID: 1, ProjectID: 1, App: db.AppBash, Playbook: "run.sh"},
		Repository: db.Repository{ID: 1, ProjectID: 1, GitURL: repositoryPath},
		prepared:   true,
		preparedArgsMap: map[string][]string{
			"default": {"run.sh"},
		},
	}
}
