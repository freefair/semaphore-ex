package tasks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
