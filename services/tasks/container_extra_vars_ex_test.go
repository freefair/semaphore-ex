package tasks

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ansible_vault"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestContainerTaskPlanBundlesTaskExtraVarsWithoutHostPathsOrPlainSecrets(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repository := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repository, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "site.yml"), []byte("---\n"), 0o600))

	unsafeValue := "{{ lookup('pipe', 'id') }}"
	plainSecret := "secret {{ do_not_evaluate }}"
	openPath := filepath.Join(root, "task_extra_vars.yml")
	secretPath := filepath.Join(root, "task_secret_vars.yml")
	require.NoError(t, os.WriteFile(openPath, []byte("message: !unsafe \""+unsafeValue+"\"\n"), 0o600))
	vaultPassword := "per-task-vault-password"
	encrypted, err := ansible_vault.Encrypt([]byte("token: !unsafe \""+plainSecret+"\"\n"), vaultPassword)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(secretPath, encrypted, 0o600))

	executor := &LocalExecutor{
		Task:     db.Task{ID: 1},
		Template: db.Template{ID: 1, ProjectID: 1, App: db.AppAnsible, Playbook: "site.yml"},
		Repository: db.Repository{
			ID: 1, ProjectID: 1, GitURL: repository,
		},
		prepared:            true,
		extraVarsFile:       openPath,
		secretVarsFile:      secretPath,
		secretVaultID:       "task-vault",
		secretVaultPassword: vaultPassword,
		preparedArgsMap: map[string][]string{"default": {
			"site.yml",
			"--vault-id=task-vault@prompt",
			"--extra-vars", "@" + openPath,
			"--extra-vars", "@" + secretPath,
		}},
	}

	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	entries := readTaskBundle(t, plan.Bundle)
	require.NoError(t, plan.Bundle.Close())
	runScript := string(entries["run.sh"])
	bundle := string(bytes.Join([][]byte{
		entries["credentials/extra-vars.yml"],
		entries["credentials/secret-vars.yml"],
		entries["credentials/secret-vars-vault-password"],
		entries["run.sh"],
	}, nil))

	assert.Contains(t, string(entries["credentials/extra-vars.yml"]), "!unsafe")
	assert.Contains(t, string(entries["credentials/extra-vars.yml"]), unsafeValue)
	assert.True(t, strings.HasPrefix(string(entries["credentials/secret-vars.yml"]), "$ANSIBLE_VAULT;"))
	assert.NotContains(t, string(entries["credentials/secret-vars.yml"]), plainSecret)
	assert.Equal(t, vaultPassword, string(entries["credentials/secret-vars-vault-password"]))
	assert.Contains(t, runScript, "@/semaphore/bundle/credentials/extra-vars.yml")
	assert.Contains(t, runScript, "@/semaphore/bundle/credentials/secret-vars.yml")
	assert.Contains(t, runScript, "--vault-id=task-vault@/semaphore/bundle/credentials/secret-vars-vault-password")
	assert.NotContains(t, runScript, vaultPassword)
	assert.NotContains(t, runScript, plainSecret)
	assert.NotContains(t, runScript, root)
	assert.NotContains(t, bundle, plainSecret)
	assert.NotContains(t, bundle, root)
}

func TestRemapContainerExtraVarsRemapsOnlyGeneratedHostPaths(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		wantErr bool
	}{
		{name: "generated runner file", args: []string{"--extra-vars", "@/tmp/task-owned.yml"}, want: []string{"--extra-vars", "@/semaphore/bundle/credentials/extra-vars.yml"}},
		{name: "relative repository file", args: []string{"--extra-vars", "@vars.yml"}, want: []string{"--extra-vars", "@/workspace/vars.yml"}},
		{name: "nested repository file", args: []string{"--extra-vars=@group/vars.yml"}, want: []string{"--extra-vars=@/workspace/group/vars.yml"}},
		{name: "normalized repository file", args: []string{"--extra-vars", "@group/../vars.yml"}, want: []string{"--extra-vars", "@/workspace/vars.yml"}},
		{name: "absolute image path", args: []string{"--extra-vars", "@/opt/site/vars.yml"}, want: []string{"--extra-vars", "@/opt/site/vars.yml"}},
		{name: "short extra vars", args: []string{"-e", "@vars.yml"}, want: []string{"-e", "@/workspace/vars.yml"}},
		{name: "parent image path", args: []string{"--extra-vars", "@../shared/vars.yml"}, want: []string{"--extra-vars", "@../shared/vars.yml"}},
		{name: "missing value", args: []string{"--extra-vars"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := remapContainerExtraVarsArgs(
				tt.args,
				map[string]string{"/tmp/task-owned.yml": "/semaphore/bundle/credentials/extra-vars.yml"},
				"",
				false,
			)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, actual)
		})
	}
}

func TestContainerTaskPlanBundlesSecretOnlyExtraVarsAndRepositoryRelativeFile(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repository := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repository, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "site.yml"), []byte("---\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "vars.yml"), []byte("from_repo: true\n"), 0o600))

	plainSecret := "secret-only-value"
	vaultPassword := "secret-only-vault-password"
	encrypted, err := ansible_vault.Encrypt([]byte("token: !unsafe \""+plainSecret+"\"\n"), vaultPassword)
	require.NoError(t, err)
	secretPath := filepath.Join(root, "task_secret_vars.yml")
	require.NoError(t, os.WriteFile(secretPath, encrypted, 0o600))
	executor := &LocalExecutor{
		Task:                db.Task{ID: 2},
		Template:            db.Template{ID: 2, ProjectID: 1, App: db.AppAnsible, Playbook: "site.yml"},
		Repository:          db.Repository{ID: 2, ProjectID: 1, GitURL: repository},
		prepared:            true,
		secretVarsFile:      secretPath,
		secretVaultID:       "secret-only",
		secretVaultPassword: vaultPassword,
		preparedArgsMap: map[string][]string{"default": {
			"site.yml", "--extra-vars=@vars.yml", "--vault-id=secret-only@prompt", "--extra-vars", "@" + secretPath,
		}},
	}

	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	entries := readTaskBundle(t, plan.Bundle)
	require.NoError(t, plan.Bundle.Close())
	runScript := string(entries["run.sh"])

	assert.Contains(t, runScript, "--extra-vars=@/workspace/vars.yml")
	assert.Contains(t, runScript, "@/semaphore/bundle/credentials/secret-vars.yml")
	assert.Contains(t, runScript, "--vault-id=secret-only@/semaphore/bundle/credentials/secret-vars-vault-password")
	assert.NotContains(t, runScript, root)
	assert.NotContains(t, runScript, plainSecret)
	assert.NotContains(t, entries, "credentials/extra-vars.yml")
	assert.True(t, strings.HasPrefix(string(entries["credentials/secret-vars.yml"]), "$ANSIBLE_VAULT;"))
	assert.NotContains(t, string(entries["credentials/secret-vars.yml"]), plainSecret)
}
