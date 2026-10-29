package tasks

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam/pamtest"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain lets the test binary act as the ProxyCommand helper, so the
// routing tests drive the real OpenSSH client end to end.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == infisicalPAMConnectSubcommand {
		flags := flag.NewFlagSet(infisicalPAMConnectSubcommand, flag.ExitOnError)
		socket := flags.String("socket", "", "")
		host := flags.String("host", "", "")
		_ = flags.Parse(os.Args[2:])
		if err := infisicalpam.Connect(context.Background(), *socket, *host, os.Stdin, os.Stdout); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "Infisical PAM:", infisicalpam.CategoryOf(err))
			os.Exit(255)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type pamRecordingLogger struct {
	task_logger.NopLogger
	lines []string
}

func (l *pamRecordingLogger) Log(message string) { l.lines = append(l.lines, message) }

func configureInfisicalPAMTest(t *testing.T) {
	t.Helper()
	// macOS limits Unix socket paths to 104 bytes; t.TempDir names exceed it.
	root, err := os.MkdirTemp("", "sx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	previous := util.Config
	util.Config = &util.ConfigType{TmpPath: root, Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingYes, KnownHostsFile: filepath.Join(root, "known_hosts")}}
	t.Cleanup(func() { util.Config = previous })
}

func infisicalPAMTestConfig(env *pamtest.Environment, hosts ...string) db.TaskInfisicalPAM {
	routes := make([]db.InfisicalPAMRoute, 0, len(hosts))
	for _, host := range hosts {
		routes = append(routes, db.InfisicalPAMRoute{Host: host, Account: "servers/" + strings.Split(host, ".")[0]})
	}
	return db.TaskInfisicalPAM{
		StorageID: 9, SiteURL: env.URL(), CACertificate: string(env.APICACert), ClientID: pamtest.ClientID,
		SessionDuration: "1h0m0s", Routes: routes, CredentialKeyID: 5,
		Credential: db.AccessKey{ID: 5, Type: db.AccessKeyString, String: pamtest.ClientSecret},
	}
}

func infisicalPAMTestExecutor(logger task_logger.Logger, configs ...db.TaskInfisicalPAM) *LocalExecutor {
	executor := &LocalExecutor{
		Task:     db.Task{ID: 77, InfisicalPAM: configs},
		Template: db.Template{ID: 1, ProjectID: 1},
		Logger:   logger,
	}
	executor.infisicalPAM.connectExecutable = os.Args[0]
	return executor
}

func runRoutedSSH(t *testing.T, executor *LocalExecutor, host, command string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executor.taskSSHRoutingCommand, "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-l", "deploy", host, command)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func TestInfisicalPAMRoutesOpenSSHWithoutTaskKeys(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	logger := &pamRecordingLogger{}
	executor := infisicalPAMTestExecutor(logger, infisicalPAMTestConfig(env, "web-1.example.test"))

	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	output, err := runRoutedSSH(t, executor, "web-1.example.test", "uptime")

	require.NoError(t, err, output)
	assert.Contains(t, output, "ok uptime")
	assert.Nil(t, executor.taskSSHAgent, "no agent is started for PAM-only routing")
	require.Len(t, env.Accesses(), 1)
	assert.Equal(t, "servers/web-1", env.Accesses()[0].Path)
	assert.Equal(t, "Semaphore EX task #77 (project #1)", env.Accesses()[0].Reason)
	assert.Contains(t, logger.lines, "Infisical PAM routes 1 SSH host(s) through storage #9")

	environment := executor.getTaskSSHAgentEnvironment(nil)
	assert.Contains(t, environment, "ANSIBLE_SSH_EXECUTABLE="+executor.taskSSHRoutingCommand)
	assert.Contains(t, environment, "ANSIBLE_SSH_CONTROL_PATH_DIR="+executor.taskSSHControlPathDir)
	assert.NotContains(t, strings.Join(environment, "\n"), pamtest.ClientSecret)
}

func TestInfisicalPAMCleanupEndsSessionsAndRemovesRouting(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	require.NoError(t, executor.startTaskSSHAgent())
	_, err := runRoutedSSH(t, executor, "web-1.example.test", "true")
	require.NoError(t, err)
	directory := filepath.Dir(executor.taskSSHRoutingCommand)

	executor.destroyKeys()

	assert.Equal(t, []string{"session-1"}, env.Ended())
	_, statErr := os.Stat(directory)
	assert.True(t, os.IsNotExist(statErr))
	assert.Empty(t, executor.taskSSHRoutingCommand)
	assert.Empty(t, executor.infisicalPAM.brokers)
}

func TestInfisicalPAMRouteOverridesHostKeyPolicyOnlyForRoutedHost(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	config := filepath.Join(filepath.Dir(executor.taskSSHRoutingCommand), "routing.conf")

	routed, err := exec.Command("ssh", "-G", "-F", config, "web-1.example.test").Output()
	require.NoError(t, err)
	other, err := exec.Command("ssh", "-G", "-F", config, "other.example.test").Output()
	require.NoError(t, err)

	assert.Contains(t, string(routed), "\nproxycommand "+posixQuote(os.Args[0])+" infisical-pam-connect --socket ")
	assert.Contains(t, string(routed), "--host web-1.example.test")
	assert.Contains(t, string(routed), "stricthostkeychecking false")
	assert.Contains(t, string(routed), "identityagent none")
	assert.Contains(t, string(other), "stricthostkeychecking true")
	assert.NotContains(t, string(other), "\nproxycommand ")
}

func TestInfisicalPAMRoutesAlongsideTaskSSHRouting(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	executor.taskSSHAgent = &ssh.Agent{SocketFile: filepath.Join(util.Config.TmpPath, "task.sock")}
	routing, err := ssh.BuildHostRouting(nil)
	require.NoError(t, err)

	require.NoError(t, executor.writeTaskSSHRoutingFiles(routing))
	t.Cleanup(executor.removeTaskSSHRoutingFiles)
	output, err := runRoutedSSH(t, executor, "web-1.example.test", "id")

	require.NoError(t, err, output)
	assert.Contains(t, output, "ok id")
	assert.Contains(t, executor.getTaskSSHAgentEnvironment(nil), "ANSIBLE_SSH_CONTROL_PATH_DIR="+executor.taskSSHControlPathDir)
}

func TestInfisicalPAMRejectsHostWithOtherCredential(t *testing.T) {
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	executor.HostConfigs = []db.HostConfig{{Type: db.HostConfigHost, Name: "WEB-1.example.test"}}

	assert.ErrorContains(t, executor.rejectInfisicalPAMConflicts(nil), "also has an SSH credential")

	executor.HostConfigs = nil
	assert.ErrorContains(t, executor.rejectInfisicalPAMConflicts([]string{"web-1.example.test"}), "also has an SSH credential")
	assert.NoError(t, executor.rejectInfisicalPAMConflicts([]string{"other.example.test"}))
}

func TestInfisicalPAMDisabledForContainerTasks(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	logger := &pamRecordingLogger{}
	executor := infisicalPAMTestExecutor(logger, infisicalPAMTestConfig(env, "web-1.example.test"))

	executor.disableInfisicalPAM("container tasks cannot reach the runner's Infisical PAM broker")
	require.NoError(t, executor.startTaskSSHAgent())

	assert.False(t, executor.infisicalPAMActive())
	assert.Empty(t, executor.taskSSHRoutingCommand)
	assert.Equal(t, []string{"Infisical PAM routes are not applied: container tasks cannot reach the runner's Infisical PAM broker"}, logger.lines)
	assert.Empty(t, env.Accesses())
}

func TestInfisicalPAMRejectsUnavailableCredential(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	config := infisicalPAMTestConfig(env, "web-1.example.test")
	config.Credential = db.AccessKey{ID: 5, Type: db.AccessKeyString}
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, config)

	err := executor.startTaskSSHAgent()

	assert.ErrorContains(t, err, "client secret is unavailable")
	assert.Empty(t, executor.taskSSHRoutingCommand)
	entries, readErr := os.ReadDir(util.Config.GetProjectTmpDir(1))
	require.NoError(t, readErr)
	assert.Empty(t, entries, "a failed start leaves no routing directory")
}

func TestInfisicalPAMHostBlockQuotesPaths(t *testing.T) {
	block := infisicalPAMHostBlock("web-1", "/opt/semaphore ex/semaphore", "/tmp/a'b/0.sock")

	assert.Contains(t, block, `ProxyCommand '/opt/semaphore ex/semaphore' infisical-pam-connect --socket '/tmp/a'"'"'b/0.sock' --host web-1`)
}

func TestInfisicalPAMForcesTaskPrivateControlPath(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)

	// A caller (Ansible or a user config) asks for a shared master socket.
	output, err := exec.Command(executor.taskSSHRoutingCommand, "-G", "-o", "ControlPath=/shared/%C", "web-1.example.test").Output()

	require.NoError(t, err)
	assert.Contains(t, string(output), "\ncontrolpath "+executor.taskSSHControlPathDir+"/")
	assert.NotContains(t, string(output), "/shared/")
}

func TestInfisicalPAMOverridesInheritedAnsibleControlPath(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	inherited := []string{"ANSIBLE_SSH_CONTROL_PATH_DIR=/shared", "ANSIBLE_SSH_CONTROL_PATH=/shared/%%h"}

	environment := append(append([]string(nil), inherited...), executor.getTaskSSHAgentEnvironment(inherited)...)

	assert.Equal(t, executor.taskSSHControlPathDir, lastEnvironmentValue(environment, "ANSIBLE_SSH_CONTROL_PATH_DIR"))
	assert.Equal(t, "%(directory)s/%%C", lastEnvironmentValue(environment, "ANSIBLE_SSH_CONTROL_PATH"))
}

func TestInfisicalPAMPassesRoutingConfigToSFTPAndSCP(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	config := filepath.Join(filepath.Dir(executor.taskSSHRoutingCommand), "routing.conf")

	assert.Equal(t, "-F "+posixQuote(config), executor.inventorySSHCommonArgs())

	// sftp starts /usr/bin/ssh itself; with -F it still reaches the broker.
	sftp, err := exec.LookPath("sftp")
	if err != nil {
		t.Skip("sftp is not installed")
	}
	output, err := exec.Command(sftp, "-o", "BatchMode=yes", "-F", config, "web-1.example.test").CombinedOutput()
	require.Error(t, err, "the stand-in target has no sftp subsystem")
	assert.NotContains(t, string(output), "Could not resolve hostname", string(output))
	assert.Eventually(t, func() bool { return len(env.Accesses()) == 1 }, 5*time.Second, 20*time.Millisecond)
}

func lastEnvironmentValue(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if strings.HasPrefix(entry, name+"=") {
			value = strings.TrimPrefix(entry, name+"=")
		}
	}
	return value
}

func TestInfisicalPAMControlPathStaysShortAndIsRemoved(t *testing.T) {
	configureInfisicalPAMTest(t)
	env := pamtest.Start(t, pamtest.Options{})
	executor := infisicalPAMTestExecutor(&pamRecordingLogger{}, infisicalPAMTestConfig(env, "web-1.example.test"))
	require.NoError(t, executor.startTaskSSHAgent())
	directory := executor.taskSSHControlPathDir
	info, err := os.Stat(directory)
	require.NoError(t, err)

	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.Less(t, len(directory)+1+40, 104, "ControlPath with %C must stay below OpenSSH's limit")

	executor.destroyKeys()
	_, err = os.Stat(directory)
	assert.True(t, os.IsNotExist(err))
}
