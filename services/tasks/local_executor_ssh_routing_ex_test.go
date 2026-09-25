package tasks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostMappingRejectsExplicitTaskRoute(t *testing.T) {
	executor := &LocalExecutor{HostConfigs: []db.HostConfig{{Type: db.HostConfigHost, Name: "git.example.test"}}}
	require.ErrorContains(t, executor.rejectHostConfigRoutingConflicts([]string{"git.example.test"}), "conflicts")
	require.NoError(t, executor.rejectHostConfigRoutingConflicts([]string{"other.example.test"}))
}

func TestURLMappingDoesNotConflictWithKeylessRepository(t *testing.T) {
	executor := &LocalExecutor{Repository: db.Repository{GitURL: "https://github.com/acme/repository"}, HostConfigs: []db.HostConfig{{Type: db.HostConfigURL, Name: "https://github.com/acme/"}}}
	require.NoError(t, executor.rejectHostConfigRoutingConflicts([]string{"inventory.example.test"}))
}

func TestTaskRoutingKeepsMappingAgentBeforeTaskDefault(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root, Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}}
	t.Cleanup(func() { util.Config = previousConfig })
	mappingConfig := filepath.Join(root, "mapping.conf")
	require.NoError(t, os.WriteFile(mappingConfig, []byte("Host mapped.example.test\n  IdentityAgent /mapping.sock\n  IdentitiesOnly no\n"), 0o600))
	executor := &LocalExecutor{taskSSHAgent: &ssh.Agent{SocketFile: filepath.Join(root, "task.sock")}, hostConfigInstallation: &ssh.HostConfigInstallation{ConfigFile: mappingConfig}}
	routing, err := ssh.BuildHostRouting(nil)
	require.NoError(t, err)
	require.NoError(t, executor.writeTaskSSHRoutingFiles(routing))
	t.Cleanup(executor.removeTaskSSHRoutingFiles)
	output, err := exec.Command("ssh", "-G", "-F", filepath.Join(root, "task.sock.routing", "routing.conf"), "mapped.example.test").Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "identityagent /mapping.sock")
	assert.Contains(t, string(output), "identitiesonly no")
}

func TestTaskSSHRoutingConfigIncludesUserBeforeSystemConfig(t *testing.T) {
	previousConfig := util.Config
	util.Config = nil
	t.Cleanup(func() { util.Config = previousConfig })
	userConfig := filepath.Join(t.TempDir(), "user.conf")
	systemConfig := filepath.Join(t.TempDir(), "system.conf")
	require.NoError(t, os.WriteFile(userConfig, []byte("Host routed.example.test\n  Port 2201\n"), 0o600))
	require.NoError(t, os.WriteFile(systemConfig, []byte("Host routed.example.test\n  Port 2202\n"), 0o600))
	config := filepath.Join(t.TempDir(), "routing.conf")
	require.NoError(t, os.WriteFile(config, []byte("Host *\n  IdentitiesOnly yes\n"+taskSSHConfigIncludes(userConfig, systemConfig)), 0o600))
	output, err := exec.Command("ssh", "-G", "-F", config, "routed.example.test").Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "port 2201")
}

func TestTaskSSHRoutingWrapperDoesNotTreatRemoteCommandFAsConfigOverride(t *testing.T) {
	directory := t.TempDir()
	fakeSSH := filepath.Join(directory, "fake-ssh")
	wrapper := filepath.Join(directory, "ssh")
	config := filepath.Join(directory, "routing.conf")
	require.NoError(t, os.WriteFile(fakeSSH, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700))
	require.NoError(t, os.WriteFile(wrapper, []byte(taskSSHRoutingWrapper(fakeSSH, config)), 0o700))
	output, err := exec.Command(wrapper, "git.example.test", "remote-command", "-F", "literal").Output()
	require.NoError(t, err)
	arguments := strings.Split(strings.TrimSpace(string(output)), "\n")
	assert.Equal(t, []string{"-F", config, "git.example.test", "remote-command", "-F", "literal"}, arguments)
}

func TestTaskSSHRoutingWrapperLetsLaterExplicitConfigOverrideGeneratedConfig(t *testing.T) {
	directory := t.TempDir()
	generated := filepath.Join(directory, "generated.conf")
	explicit := filepath.Join(directory, "explicit.conf")
	wrapper := filepath.Join(directory, "ssh")
	sshBinary, err := exec.LookPath("ssh")
	require.NoError(t, err)
	sshBinary, err = filepath.Abs(sshBinary)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(generated, []byte("Host target.example.test\n  Port 2201\n"), 0o600))
	require.NoError(t, os.WriteFile(explicit, []byte("Host target.example.test\n  Port 2202\n"), 0o600))
	require.NoError(t, os.WriteFile(wrapper, []byte(taskSSHRoutingWrapper(sshBinary, generated)), 0o700))
	output, err := exec.Command(wrapper, "-G", "-F", explicit, "target.example.test").Output()
	require.NoError(t, err)
	assert.Contains(t, string(output), "port 2202")
}

func TestRepositorySSHHostsCanonicalizesDNSBeforeRoutingValidation(t *testing.T) {
	assert.Equal(t, []string{"github.com"}, repositorySSHHosts("ssh://git@GitHub.COM:2222/owner/repository.git"))
	assert.Equal(t, []string{"github.com"}, repositorySSHHosts("git@GitHub.COM:owner/repository.git"))
}
