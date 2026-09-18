package tasks

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sshcrypto "golang.org/x/crypto/ssh"
)

func TestLocalExecutorRepositoryAgentAuthenticatesNestedGit(t *testing.T) {
	configureRepositoryAgentTest(t)
	repositoryPrivateKey, repositorySigner := repositoryAgentTestPrivateKey(t)
	inventoryPrivateKey, inventorySigner := repositoryAgentTestPrivateKey(t)
	server := startRepositoryAgentGitServer(t, repositorySigner.PublicKey(), createRepositoryAgentFixture(t, "repository"))

	executor := repositoryAgentTestExecutor(repositoryPrivateKey, inventoryPrivateKey)
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)

	environment := executor.getTaskSSHAgentEnvironment()
	require.Contains(t, environment, "SSH_AUTH_SOCK="+executor.taskSSHAgent.SocketFile)
	gitIdentities := append(append([]string(nil), executor.repositorySSHIdentityFiles...), executor.inventorySSHIdentityFiles...)
	require.Contains(t, environment, "GIT_SSH_COMMAND="+ssh.TaskGitSSHCommand(executor.taskSSHAgent.SocketFile, gitIdentities))
	require.NotContains(t, environment, "ANSIBLE_SSH_ARGS=")

	identities := runRepositoryAgentCommand(t, "", taskEnvironment(environment), "ssh-add", "-l")
	assert.Contains(t, identities, sshcrypto.FingerprintSHA256(repositorySigner.PublicKey()))
	assert.Contains(t, identities, sshcrypto.FingerprintSHA256(inventorySigner.PublicKey()))

	baseline := runRepositoryAgentCommandError(t, "", repositoryAgentMissingEnvironment(), "git", "ls-remote", server.url)
	assert.Contains(t, baseline, "Permission denied", "the nested Git operation must fail without the task repository-agent environment")

	output := runRepositoryAgentCommand(t, "", taskEnvironment(environment), "git", "ls-remote", server.url)
	assert.NotEmpty(t, output, "the seeded repository must expose a reference through the task agent")
}

func TestLocalExecutorRepositoryAgentAuthenticatesNestedGitWithInventoryKey(t *testing.T) {
	configureRepositoryAgentTest(t)
	repositoryPrivateKey, _ := repositoryAgentTestPrivateKey(t)
	inventoryPrivateKey, inventorySigner := repositoryAgentTestPrivateKey(t)
	server := startRepositoryAgentGitServer(t, inventorySigner.PublicKey(), createRepositoryAgentFixture(t, "inventory-key"))

	executor := repositoryAgentTestExecutor(repositoryPrivateKey, inventoryPrivateKey)
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)

	output := runRepositoryAgentCommand(t, "", taskEnvironment(executor.getTaskSSHAgentEnvironment()), "git", "ls-remote", server.url)
	assert.NotEmpty(t, output, "a dependency authorized by inventory key I must remain reachable when repository key K is also loaded")
}

func TestLocalExecutorRepositoryAgentCleanupRemovesSocketAndSelectors(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "cancellation"} {
		t.Run(outcome, func(t *testing.T) {
			configureRepositoryAgentTest(t)
			repositoryPrivateKey, repositorySigner := repositoryAgentTestPrivateKey(t)
			server := startRepositoryAgentGitServer(t, repositorySigner.PublicKey(), createRepositoryAgentFixture(t, "cleanup-"+outcome))
			executor := repositoryAgentTestExecutor(repositoryPrivateKey, "")
			require.NoError(t, executor.startTaskSSHAgent())
			t.Cleanup(executor.destroyKeys)

			socketFile := executor.taskSSHAgent.SocketFile
			selectorFiles := append([]string(nil), executor.taskSSHIdentityFiles...)
			switch outcome {
			case "success":
				runRepositoryAgentCommand(t, "", taskEnvironment(executor.getTaskSSHAgentEnvironment()), "git", "ls-remote", server.url)
			case "failure":
				runRepositoryAgentCommandError(t, "", repositoryAgentMissingEnvironment(), "git", "ls-remote", server.url)
			case "cancellation":
				executor.Kill()
			}

			executor.destroyKeys()
			_, err := os.Stat(socketFile)
			assert.True(t, os.IsNotExist(err), "the task agent socket must be removed after %s", outcome)
			for _, selector := range selectorFiles {
				_, err = os.Stat(selector)
				assert.True(t, os.IsNotExist(err), "the public selector must be removed after %s", outcome)
			}
		})
	}
}

func TestLocalExecutorRepositoryAgentCommandLineIdentityOverridesSSHConfig(t *testing.T) {
	configureRepositoryAgentTest(t)
	repositoryPrivateKey, repositorySigner := repositoryAgentTestPrivateKey(t)
	server := startRepositoryAgentGitServer(t, repositorySigner.PublicKey(), createRepositoryAgentFixture(t, "configured-agent"))
	executor := repositoryAgentTestExecutor(repositoryPrivateKey, "")
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)

	homeDirectory := t.TempDir()
	sshDirectory := filepath.Join(homeDirectory, ".ssh")
	require.NoError(t, os.Mkdir(sshDirectory, 0o700))
	configFile := filepath.Join(sshDirectory, "config")
	require.NoError(t, os.WriteFile(configFile, []byte("Host 127.0.0.1\n  IdentityAgent none\n  StrictHostKeyChecking no\n  UserKnownHostsFile /dev/null\n"), 0o600))

	output := runRepositoryAgentCommand(t, "", repositoryAgentConfiguredEnvironment(executor.getTaskSSHAgentEnvironment(), configFile, "HOME="+homeDirectory), "git", "ls-remote", server.url)
	assert.NotEmpty(t, output, "the command-line IdentityAgent must override the conflicting SSH config IdentityAgent")
}

func TestLocalExecutorRepositoryAgentExternalToolIntegrations(t *testing.T) {
	if os.Getenv("SEMAPHORE_RUN_REPOSITORY_KEY_TOOL_INTEGRATION") != "1" {
		t.Skip("set SEMAPHORE_RUN_REPOSITORY_KEY_TOOL_INTEGRATION=1 to exercise locally installed Ansible and Terraform")
	}

	configureRepositoryAgentTest(t)
	repositoryPrivateKey, repositorySigner := repositoryAgentTestPrivateKey(t)
	executor := repositoryAgentTestExecutor(repositoryPrivateKey, "")
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	environment := taskEnvironment(executor.getTaskSSHAgentEnvironment())

	t.Run("ansible galaxy installs a Git collection", func(t *testing.T) {
		repository := createAnsibleCollectionFixture(t)
		server := startRepositoryAgentGitServer(t, repositorySigner.PublicKey(), repository)
		collections := filepath.Join(t.TempDir(), "collections")
		runRepositoryAgentCommand(t, "", environment, "ansible-galaxy", "collection", "install", "git+"+server.url, "-p", collections)
	})

	t.Run("terraform initializes a Git module", func(t *testing.T) {
		repository := createTerraformModuleFixture(t)
		server := startRepositoryAgentGitServer(t, repositorySigner.PublicKey(), repository)
		workingDirectory := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, "main.tf"), []byte("module \"fixture\" {\n  source = \"git::"+server.url+"\"\n}\n"), 0o600))
		runRepositoryAgentCommand(t, workingDirectory, environment, "terraform", "init", "-backend=false", "-input=false")
	})

	t.Run("ansible managed host selects the inventory key", func(t *testing.T) {
		inventoryPrivateKey, inventorySigner := repositoryAgentTestPrivateKey(t)
		managedExecutor := repositoryAgentTestExecutor(repositoryPrivateKey, inventoryPrivateKey)
		require.NoError(t, managedExecutor.startTaskSSHAgent())
		t.Cleanup(managedExecutor.destroyKeys)
		server := startRepositoryAgentGitServer(t, inventorySigner.PublicKey(), createRepositoryAgentFixture(t, "managed-host"))
		inventory := filepath.Join(t.TempDir(), "inventory.ini")
		require.NoError(t, os.WriteFile(inventory, []byte("[managed]\nloopback ansible_host=127.0.0.1 ansible_port="+strconv.Itoa(server.port)+" ansible_user=git ansible_ssh_common_args='-F none -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'\n"), 0o600))
		ansibleHome := t.TempDir()
		controlPathDirectory := filepath.Join(util.Config.TmpPath, "cp")
		require.NoError(t, os.Mkdir(controlPathDirectory, 0o700))
		ansibleEnvironment := repositoryAgentRuntimeEnvironment(managedExecutor.getTaskSSHAgentEnvironment(), "HOME="+ansibleHome, "ANSIBLE_LOCAL_TEMP="+filepath.Join(ansibleHome, "local"), "ANSIBLE_REMOTE_TMP=/tmp", "ANSIBLE_SSH_CONTROL_PATH_DIR="+controlPathDirectory)
		output := runRepositoryAgentCommand(t, "", ansibleEnvironment, "ansible", "-i", inventory, "managed", "-m", "raw", "-a", "echo semaphore-inventory-marker")
		assert.Contains(t, output, "semaphore-inventory-marker")
	})
}

func configureRepositoryAgentTest(t *testing.T) {
	t.Helper()
	temporaryDirectory, err := os.MkdirTemp("/tmp", "rka")
	require.NoError(t, err)
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: temporaryDirectory}
	t.Cleanup(func() {
		util.Config = previousConfig
		require.NoError(t, os.RemoveAll(temporaryDirectory))
	})
}

func repositoryAgentTestExecutor(repositoryPrivateKey, inventoryPrivateKey string) *LocalExecutor {
	executor := &LocalExecutor{
		Template:   db.Template{ProjectID: 1},
		Repository: db.Repository{SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: repositoryPrivateKey}}},
		Logger:     task_logger.NopLogger{},
	}
	if inventoryPrivateKey != "" {
		executor.Inventory.SSHKey = db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: inventoryPrivateKey}}
	}
	return executor
}

func repositoryAgentTestPrivateKey(t *testing.T) (string, sshcrypto.Signer) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	signer, err := sshcrypto.ParsePrivateKey(pemKey)
	require.NoError(t, err)
	return string(pemKey), signer
}

func createRepositoryAgentFixture(t *testing.T, name string) string {
	t.Helper()
	workingDirectory := t.TempDir()
	repository := filepath.Join(t.TempDir(), name+".git")
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "init")
	require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, "README.md"), []byte("fixture\n"), 0o600))
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "add", "README.md")
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "-c", "user.name=Semaphore Test", "-c", "user.email=semaphore-test@example.invalid", "commit", "-m", "fixture")
	runRepositoryAgentCommand(t, "", nil, "git", "init", "--bare", repository)
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "remote", "add", "origin", repository)
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "push", "origin", "HEAD")
	return repository
}

func createAnsibleCollectionFixture(t *testing.T) string {
	t.Helper()
	workingDirectory := t.TempDir()
	repository := filepath.Join(t.TempDir(), "collection.git")
	require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, "galaxy.yml"), []byte("namespace: semaphore\nname: fixture\nversion: 1.0.0\nreadme: README.md\nauthors:\n  - Semaphore\ndescription: Local test fixture\nlicense:\n  - MIT\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, "README.md"), []byte("fixture\n"), 0o600))
	pushRepositoryAgentFixture(t, workingDirectory, repository)
	return repository
}

func createTerraformModuleFixture(t *testing.T) string {
	t.Helper()
	workingDirectory := t.TempDir()
	repository := filepath.Join(t.TempDir(), "terraform-module.git")
	require.NoError(t, os.WriteFile(filepath.Join(workingDirectory, "main.tf"), []byte("output \"fixture\" {\n  value = \"ok\"\n}\n"), 0o600))
	pushRepositoryAgentFixture(t, workingDirectory, repository)
	return repository
}

func pushRepositoryAgentFixture(t *testing.T, workingDirectory, repository string) {
	t.Helper()
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "init")
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "add", ".")
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "-c", "user.name=Semaphore Test", "-c", "user.email=semaphore-test@example.invalid", "commit", "-m", "fixture")
	runRepositoryAgentCommand(t, "", nil, "git", "init", "--bare", repository)
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "remote", "add", "origin", repository)
	runRepositoryAgentCommand(t, workingDirectory, nil, "git", "push", "origin", "HEAD")
}

type repositoryAgentGitServer struct {
	url  string
	port int
}

func startRepositoryAgentGitServer(t *testing.T, allowedKey sshcrypto.PublicKey, repository string) repositoryAgentGitServer {
	t.Helper()
	_, hostSigner := repositoryAgentTestPrivateKey(t)
	config := &sshcrypto.ServerConfig{
		PublicKeyCallback: func(_ sshcrypto.ConnMetadata, key sshcrypto.PublicKey) (*sshcrypto.Permissions, error) {
			if bytes.Equal(key.Marshal(), allowedKey.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("repository key required")
		},
	}
	config.AddHostKey(hostSigner)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().(*net.TCPAddr)
	t.Cleanup(func() { require.NoError(t, listener.Close()) })

	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveRepositoryAgentGitConnection(connection, config, repository)
		}
	}()

	return repositoryAgentGitServer{url: "ssh://git@127.0.0.1:" + strconv.Itoa(address.Port) + repository, port: address.Port}
}

func serveRepositoryAgentGitConnection(connection net.Conn, config *sshcrypto.ServerConfig, repository string) {
	serverConnection, channels, requests, err := sshcrypto.NewServerConn(connection, config)
	if err != nil {
		return
	}
	defer serverConnection.Close()
	go sshcrypto.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(sshcrypto.UnknownChannelType, "only session channels are supported")
			continue
		}
		session, requests, acceptErr := channelRequest.Accept()
		if acceptErr != nil {
			continue
		}
		for request := range requests {
			if request.Type == "pty-req" || request.Type == "env" {
				_ = request.Reply(true, nil)
				continue
			}
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			status := runRepositoryAgentGitUploadPack(session, request.Payload, repository)
			_, _ = session.SendRequest("exit-status", false, sshcrypto.Marshal(struct{ Status uint32 }{uint32(status)}))
			_ = session.Close()
			break
		}
	}
}

func runRepositoryAgentGitUploadPack(session sshcrypto.Channel, payload []byte, repository string) int {
	var request struct{ Command string }
	if err := sshcrypto.Unmarshal(payload, &request); err != nil {
		return 127
	}
	if strings.Contains(request.Command, "echo semaphore-inventory-marker") {
		_, _ = session.Write([]byte("semaphore-inventory-marker\n"))
		return 0
	}
	argument := strings.TrimPrefix(request.Command, "git-upload-pack ")
	argument = strings.Trim(argument, "'")
	if argument != repository {
		return 127
	}
	command := exec.Command("git-upload-pack", repository)
	command.Stdin = session
	command.Stdout = session
	command.Stderr = session.Stderr()
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		return 1
	}
	return 0
}

func taskEnvironment(environment []string) []string {
	result := repositoryAgentRuntimeEnvironment(environment)
	for _, variable := range environment {
		if strings.HasPrefix(variable, "GIT_SSH_COMMAND=") && !strings.Contains(variable, "-F none") {
			for index, configured := range result {
				if configured == variable {
					result[index] = variable + " -F none -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
				}
			}
		}
	}
	return result
}

func repositoryAgentRuntimeEnvironment(environment []string, additional ...string) []string {
	result := append([]string{}, os.Environ()...)
	result = append(result, additional...)
	return append(result, environment...)
}

func repositoryAgentConfiguredEnvironment(environment []string, configFile string, additional ...string) []string {
	result := repositoryAgentRuntimeEnvironment(environment, additional...)
	for index, variable := range result {
		if strings.HasPrefix(variable, "GIT_SSH_COMMAND=") {
			result[index] = variable + " -F " + strconv.Quote(configFile)
		}
	}
	return result
}

func repositoryAgentMissingEnvironment() []string {
	return append(os.Environ(), "GIT_SSH_COMMAND=ssh -F none -o BatchMode=yes -o IdentitiesOnly=yes -o IdentityAgent=none -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null")
}

func runRepositoryAgentCommand(t *testing.T, directory string, environment []string, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	if environment != nil {
		command.Env = environment
	}
	output, err := command.CombinedOutput()
	require.NoErrorf(t, err, "%s %s failed: %s", name, strings.Join(arguments, " "), output)
	return string(output)
}

func runRepositoryAgentCommandError(t *testing.T, directory string, environment []string, name string, arguments ...string) string {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = directory
	command.Env = environment
	output, err := command.CombinedOutput()
	require.Errorf(t, err, "%s %s unexpectedly succeeded: %s", name, strings.Join(arguments, " "), output)
	return string(output)
}
