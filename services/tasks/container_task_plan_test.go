package tasks

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/pkg/task_logger"
	"github.com/semaphoreui/semaphore/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sshcrypto "golang.org/x/crypto/ssh"
)

func TestContainerTaskBootstrapLoadsRepositoryAndInventoryKeys(t *testing.T) {
	if os.Getenv("SEMAPHORE_RUN_CONTAINER_SSH_INTEGRATION") != "1" {
		t.Skip("set SEMAPHORE_RUN_CONTAINER_SSH_INTEGRATION=1 to run Docker SSH-agent integration")
	}
	root := t.TempDir()
	agentTmp, err := os.MkdirTemp("/tmp", "cts")
	require.NoError(t, err)
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: agentTmp, Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}}
	t.Cleanup(func() { util.Config = previousConfig; require.NoError(t, os.RemoveAll(agentTmp)) })
	repositoryKey, repositorySigner := repositoryAgentTestEncryptedPrivateKey(t, "container-repository-passphrase")
	inventoryKey, inventorySigner := repositoryAgentTestPrivateKey(t)
	extraKey, extraSigner := repositoryAgentTestEncryptedPrivateKey(t, "container-extra-passphrase")
	extraTwoKey, extraTwoSigner := repositoryAgentTestEncryptedPrivateKey(t, "container-extra-two-passphrase")
	extraThreeKey, extraThreeSigner := repositoryAgentTestEncryptedPrivateKey(t, "container-extra-three-passphrase")
	server := startRepositoryAgentGitServerForContainer(t, repositorySigner.PublicKey(), createRepositoryAgentFixture(t, "container-repository"))
	repository := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repository, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "run.sh"), []byte("#!/bin/sh\nset -eu\nif ssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/workspace/.semaphore/known_hosts -p \"${SSH_TEST_SERVER_PORT}\" git@unknown.example.test 'echo unexpected'; then exit 97; fi\nssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/workspace/.semaphore/known_hosts -p \"${SSH_TEST_SERVER_PORT}\" git@host.docker.internal 'echo semaphore-inventory-marker'\ngit ls-remote \"${SSH_TEST_SERVER_URL}\"\nssh-add -l\n"), 0o700))
	executor := &LocalExecutor{Task: db.Task{ID: 1, ResolvedSSHKeys: []db.ResolvedTaskSSHKey{{Binding: db.SSHKeyBinding{AccessKeyID: 40, Hosts: []string{"host.docker.internal"}}, Key: db.AccessKey{ID: 40, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: repositoryKey, Passphrase: "container-repository-passphrase"}}}, {Binding: db.SSHKeyBinding{AccessKeyID: 42, Hosts: []string{"inventory.example.test"}}, Key: db.AccessKey{ID: 42, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: inventoryKey}}}, {Binding: db.SSHKeyBinding{AccessKeyID: 41, Hosts: []string{"extra.example.test"}}, Key: db.AccessKey{ID: 41, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: extraKey, Passphrase: "container-extra-passphrase"}}}, {Binding: db.SSHKeyBinding{AccessKeyID: 43, Hosts: []string{"extra-two.example.test"}}, Key: db.AccessKey{ID: 43, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: extraTwoKey, Passphrase: "container-extra-two-passphrase"}}}, {Binding: db.SSHKeyBinding{AccessKeyID: 44, Hosts: []string{"extra-three.example.test"}}, Key: db.AccessKey{ID: 44, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: extraThreeKey, Passphrase: "container-extra-three-passphrase"}}}}}, Template: db.Template{ID: 1, ProjectID: 1, App: db.AppBash, Playbook: "run.sh"}, Repository: db.Repository{ID: 1, ProjectID: 1, GitURL: repository, SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: repositoryKey, Passphrase: "container-repository-passphrase"}}}, Inventory: db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: inventoryKey}}}, prepared: true, preparedEnv: []string{"SSH_TEST_SERVER_URL=" + server.url, "SSH_TEST_SERVER_PORT=" + strconv.Itoa(server.port)}, preparedArgsMap: map[string][]string{"default": {"run.sh"}}}
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	executor.preparedEnv = append(executor.preparedEnv, executor.getTaskSSHAgentEnvironment(executor.preparedEnv)...)
	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	bundle, err := io.ReadAll(plan.Bundle)
	require.NoError(t, err)
	require.NoError(t, plan.Bundle.Close())
	bundleDir := filepath.Join(root, "bundle")
	require.NoError(t, os.Mkdir(bundleDir, 0o700))
	archive := tar.NewReader(bytes.NewReader(bundle))
	for {
		header, nextErr := archive.Next()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		target := filepath.Join(bundleDir, header.Name)
		if header.FileInfo().IsDir() {
			require.NoError(t, os.MkdirAll(target, 0o700))
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
		contents, readErr := io.ReadAll(archive)
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(target, contents, os.FileMode(header.Mode)))
	}
	workspace := filepath.Join(root, "workspace")
	require.NoError(t, os.Mkdir(workspace, 0o700))
	command := exec.Command("docker", "run", "--rm", "--add-host", "unknown.example.test:host-gateway", "-v", bundleDir+":/semaphore/bundle:ro", "-v", workspace+":/workspace", "ghcr.io/freefair/semaphore-ex-runner:latest", "/bin/sh", "-c", "/semaphore/bundle/run.sh bootstrap; /semaphore/bundle/run.sh run")
	output, err := command.CombinedOutput()
	logDirectory := "/tmp/semaphore-task-agent-release-security"
	require.NoError(t, os.MkdirAll(logDirectory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(logDirectory, "container-key-auth.log"), output, 0o600))
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "HEAD")
	assert.Contains(t, string(output), "semaphore-inventory-marker")
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(repositorySigner.PublicKey()))
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(inventorySigner.PublicKey()))
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(extraSigner.PublicKey()))
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(extraTwoSigner.PublicKey()))
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(extraThreeSigner.PublicKey()))
	assert.Equal(t, []string{sshcrypto.FingerprintSHA256(repositorySigner.PublicKey()), sshcrypto.FingerprintSHA256(repositorySigner.PublicKey())}, server.attemptedPublicKeys())
}

func TestContainerTaskBootstrapBelowRoutingThresholdPreservesInventoryPriority(t *testing.T) {
	if os.Getenv("SEMAPHORE_RUN_CONTAINER_SSH_INTEGRATION") != "1" {
		t.Skip("set SEMAPHORE_RUN_CONTAINER_SSH_INTEGRATION=1 to run Docker SSH-agent integration")
	}
	root := t.TempDir()
	agentTmp, err := os.MkdirTemp("/tmp", "cts")
	require.NoError(t, err)
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: agentTmp, Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}}
	t.Cleanup(func() { util.Config = previousConfig; require.NoError(t, os.RemoveAll(agentTmp)) })
	inventoryKey, inventorySigner := repositoryAgentTestPrivateKey(t)
	repositoryKey, repositorySigner := repositoryAgentTestEncryptedPrivateKey(t, "below-threshold-repository-passphrase")
	extraKey, extraSigner := repositoryAgentTestEncryptedPrivateKey(t, "below-threshold-extra-passphrase")
	inventoryServer := startRepositoryAgentGitServerForContainer(t, inventorySigner.PublicKey(), createRepositoryAgentFixture(t, "below-threshold-inventory"))
	extraServer := startRepositoryAgentGitServerAtWithMaxAuthTries(t, "0.0.0.0", "host.docker.internal", extraSigner.PublicKey(), createRepositoryAgentFixture(t, "below-threshold-extra"), 0)
	repository := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repository, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "run.sh"), []byte("#!/bin/sh\nset -eu\nssh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/workspace/.semaphore/known_hosts -p \"${SSH_INVENTORY_PORT}\" git@host.docker.internal 'echo semaphore-inventory-marker'\ngit ls-remote \"${SSH_EXTRA_SERVER_URL}\"\nssh-add -l\n"), 0o700))
	executor := &LocalExecutor{Task: db.Task{ID: 2, ResolvedSSHKeys: []db.ResolvedTaskSSHKey{{Binding: db.SSHKeyBinding{AccessKeyID: 51}, Key: db.AccessKey{ID: 51, Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: extraKey, Passphrase: "below-threshold-extra-passphrase"}}}}}, Template: db.Template{ID: 2, ProjectID: 1, App: db.AppBash, Playbook: "run.sh"}, Repository: db.Repository{ID: 2, ProjectID: 1, GitURL: repository, SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: repositoryKey, Passphrase: "below-threshold-repository-passphrase"}}}, Inventory: db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: inventoryKey}}}, prepared: true, preparedEnv: []string{"SSH_INVENTORY_PORT=" + strconv.Itoa(inventoryServer.port), "SSH_EXTRA_SERVER_URL=" + extraServer.url}, preparedArgsMap: map[string][]string{"default": {"run.sh"}}}
	require.NoError(t, executor.startTaskSSHAgent())
	t.Cleanup(executor.destroyKeys)
	executor.preparedEnv = append(executor.preparedEnv, executor.getTaskSSHAgentEnvironment(executor.preparedEnv)...)
	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	bundle, err := io.ReadAll(plan.Bundle)
	require.NoError(t, err)
	require.NoError(t, plan.Bundle.Close())
	bundleDir := filepath.Join(root, "bundle")
	require.NoError(t, os.Mkdir(bundleDir, 0o700))
	archive := tar.NewReader(bytes.NewReader(bundle))
	for {
		header, nextErr := archive.Next()
		if nextErr == io.EOF {
			break
		}
		require.NoError(t, nextErr)
		target := filepath.Join(bundleDir, header.Name)
		if header.FileInfo().IsDir() {
			require.NoError(t, os.MkdirAll(target, 0o700))
			continue
		}
		require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
		contents, readErr := io.ReadAll(archive)
		require.NoError(t, readErr)
		require.NoError(t, os.WriteFile(target, contents, os.FileMode(header.Mode)))
	}
	workspace := filepath.Join(root, "workspace")
	require.NoError(t, os.Mkdir(workspace, 0o700))
	command := exec.Command("docker", "run", "--rm", "-v", bundleDir+":/semaphore/bundle:ro", "-v", workspace+":/workspace", "ghcr.io/freefair/semaphore-ex-runner:latest", "/bin/sh", "-c", "/semaphore/bundle/run.sh bootstrap; /semaphore/bundle/run.sh run")
	output, err := command.CombinedOutput()
	logDirectory := "/tmp/semaphore-task-agent-release-security"
	require.NoError(t, os.MkdirAll(logDirectory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(logDirectory, "container-key-below-threshold.log"), output, 0o600))
	require.NoError(t, err, string(output))
	assert.Contains(t, string(output), "semaphore-inventory-marker")
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(repositorySigner.PublicKey()))
	assert.Contains(t, string(output), sshcrypto.FingerprintSHA256(extraSigner.PublicKey()))
	identities := string(output)
	assert.Less(t, strings.Index(identities, sshcrypto.FingerprintSHA256(inventorySigner.PublicKey())), strings.Index(identities, sshcrypto.FingerprintSHA256(repositorySigner.PublicKey())))
	assert.Equal(t, []string{sshcrypto.FingerprintSHA256(inventorySigner.PublicKey())}, inventoryServer.attemptedPublicKeys())
}

func TestContainerTaskPlanUsesPublicSelectorsAndPreservesExplicitSSHSettings(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repositoryKey, repositorySigner := repositoryAgentTestPrivateKey(t)
	inventoryKey, inventorySigner := repositoryAgentTestPrivateKey(t)
	repository := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repository, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "run.sh"), []byte("#!/bin/sh\n"), 0o700))
	executor := &LocalExecutor{Task: db.Task{ID: 1}, Template: db.Template{ID: 1, ProjectID: 1, App: db.AppBash, Playbook: "run.sh"}, Repository: db.Repository{ID: 1, ProjectID: 1, GitURL: repository, SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: repositoryKey}}}, Inventory: db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: inventoryKey}}}, prepared: true, preparedEnv: []string{"GIT_SSH_COMMAND=ssh -F /custom/ssh_config", "ANSIBLE_PRIVATE_KEY_FILE=/custom/inventory.pub", "ANSIBLE_SSH_ARGS=-o ControlMaster=auto"}, preparedArgsMap: map[string][]string{"default": {"run.sh"}}}

	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	entries := readTaskBundle(t, plan.Bundle)
	require.NoError(t, plan.Bundle.Close())

	assert.Equal(t, sshcrypto.MarshalAuthorizedKey(repositorySigner.PublicKey()), entries["credentials/ssh-key-repository.pub"])
	assert.Equal(t, sshcrypto.MarshalAuthorizedKey(inventorySigner.PublicKey()), entries["credentials/ssh-key-inventory.pub"])
	runScript := string(entries["run.sh"])
	assert.Contains(t, runScript, "${bundle_dir}/ssh-route.sh")
	assert.Contains(t, runScript, "if [ -z \"${GIT_SSH_COMMAND+x}\" ]")
	assert.Contains(t, runScript, "if [ -z \"${ANSIBLE_SSH_EXECUTABLE+x}\" ]")
	assert.Contains(t, string(entries["credentials/ssh-route.conf"]), "IdentityAgent \"/workspace/.semaphore/ssh-agent.sock\"")
	assert.Contains(t, string(entries["credentials/ssh-route.conf"]), "Include \"~/.ssh/config\"")
	assert.Contains(t, string(entries["credentials/ssh-route.conf"]), "Include \"/etc/ssh/ssh_config\"")
	assert.Contains(t, string(entries["ssh-route.sh"]), "-F /semaphore/bundle/credentials/ssh-route.conf \"$@\"")
	assert.Contains(t, string(entries["ssh-bin/ssh"]), "-F /semaphore/bundle/credentials/ssh-route.conf \"$@\"")
	assert.NotContains(t, entries, "ssh")
	assert.Contains(t, runScript, "export PATH=\"${bundle_dir}/ssh-bin:${runner_path}\"")
	assert.Contains(t, runScript, "semaphore_system_ssh=\"$(command -v ssh)\"")
	assert.Contains(t, string(entries["credentials/environment.sh"]), "export GIT_SSH_COMMAND='ssh -F /custom/ssh_config'")
	assert.Contains(t, string(entries["credentials/environment.sh"]), "export ANSIBLE_PRIVATE_KEY_FILE='/custom/inventory.pub'")
	assert.Contains(t, string(entries["credentials/environment.sh"]), "export ANSIBLE_SSH_ARGS='-o ControlMaster=auto'")
	for name := range entries {
		assert.NotContains(t, name, "workspace")
	}
}

func TestContainerTaskPlanKeepsAgentlessGitHostKeyPolicy(t *testing.T) {
	root := t.TempDir()
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: root}
	t.Cleanup(func() { util.Config = previousConfig })
	repository := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repository, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repository, "run.sh"), []byte("#!/bin/sh\n"), 0o700))
	executor := &LocalExecutor{
		Task:            db.Task{ID: 3},
		Template:        db.Template{ID: 3, ProjectID: 1, App: db.AppBash, Playbook: "run.sh"},
		Repository:      db.Repository{ID: 3, ProjectID: 1, GitURL: repository},
		prepared:        true,
		preparedEnv:     []string{"GIT_SSH_COMMAND=ssh -F /custom/ssh_config"},
		preparedArgsMap: map[string][]string{"default": {"run.sh"}},
	}

	plan, err := executor.ContainerTaskPlan()
	require.NoError(t, err)
	entries := readTaskBundle(t, plan.Bundle)
	require.NoError(t, plan.Bundle.Close())

	runScript := string(entries["run.sh"])
	assert.Contains(t, runScript, "elif [ -z \"${GIT_SSH_COMMAND+x}\" ]; then\n    export GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/workspace/.semaphore/known_hosts'")
	assert.Contains(t, string(entries["credentials/environment.sh"]), "export GIT_SSH_COMMAND='ssh -F /custom/ssh_config'")
}

func TestContainerEnvironmentDropsGeneratedTaskRoutingVariables(t *testing.T) {
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir()}
	t.Cleanup(func() { util.Config = previousConfig })
	executor := LocalExecutor{
		Template:                   db.Template{ID: 1},
		Repository:                 db.Repository{ID: 1},
		taskSSHAgent:               &ssh.Agent{SocketFile: "/tmp/task-agent.sock"},
		taskSSHRoutingCommand:      "/tmp/task-routing/ssh",
		repositorySSHIdentityFiles: []string{"/tmp/repository.pub"},
		extraSSHIdentityFiles:      []string{"/tmp/extra.pub"},
	}
	executor.preparedEnv = append([]string{"GIT_SSH_COMMAND=ssh -F /custom/ssh", "ANSIBLE_SSH_EXECUTABLE=/custom/ssh"}, executor.getTaskSSHAgentEnvironment()...)
	environment, err := executor.containerEnvironment()
	require.NoError(t, err)
	assert.Contains(t, environment, "GIT_SSH_COMMAND=ssh -F /custom/ssh")
	assert.Contains(t, environment, "ANSIBLE_SSH_EXECUTABLE=/custom/ssh")
	assert.NotContains(t, environment, "GIT_SSH_COMMAND="+posixQuote(executor.taskSSHRoutingCommand))
	assert.NotContains(t, environment, "ANSIBLE_SSH_EXECUTABLE="+executor.taskSSHRoutingCommand)
}

func TestContainerTaskCredentialsRequireRoutesForFiveDistinctKeys(t *testing.T) {
	keys := make([]string, 5)
	for index := range keys {
		keys[index], _ = repositoryAgentTestPrivateKey(t)
	}
	executor := &LocalExecutor{
		Repository: db.Repository{GitURL: "ssh://git@repository.example.test/repository.git", SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: keys[0]}}},
		Inventory:  db.Inventory{SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: keys[1]}}},
		Task: db.Task{ResolvedSSHKeys: []db.ResolvedTaskSSHKey{
			{Binding: db.SSHKeyBinding{AccessKeyID: 2, Hosts: []string{"inventory.example.test"}}, Key: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: keys[1]}}},
			{Binding: db.SSHKeyBinding{AccessKeyID: 3, Hosts: []string{"extra-3.example.test"}}, Key: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: keys[2]}}},
			{Binding: db.SSHKeyBinding{AccessKeyID: 4, Hosts: []string{"extra-4.example.test"}}, Key: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: keys[3]}}},
			{Binding: db.SSHKeyBinding{AccessKeyID: 5, Hosts: []string{"extra-5.example.test"}}, Key: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: keys[4]}}},
		}},
	}
	files, err := executor.prepareContainerCredentials(map[string][]string{"default": {}})
	require.NoError(t, err)
	entries := make(map[string][]byte, len(files))
	for _, file := range files {
		entries[file.name] = file.data
	}
	assert.Contains(t, string(entries["credentials/ssh-route.conf"]), "Match final host repository.example.test")
	assert.Contains(t, string(entries["credentials/ssh-route.conf"]), "Match final host inventory.example.test")
	assert.Contains(t, string(entries["credentials/ssh-route.conf"]), "IdentityFile none")

	executor.Task.ResolvedSSHKeys = executor.Task.ResolvedSSHKeys[1:]
	_, err = executor.prepareContainerCredentials(map[string][]string{"default": {}})
	assert.ErrorContains(t, err, "requires an explicit host route")
}

func TestContainerCredentialsIncludeHostAndURLMappings(t *testing.T) {
	previousConfig := util.Config
	util.Config = &util.ConfigType{TmpPath: t.TempDir(), Ssh: &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingNo}}
	t.Cleanup(func() { util.Config = previousConfig })
	key, _ := repositoryAgentTestPrivateKey(t)
	executor := &LocalExecutor{Template: db.Template{ProjectID: 1}, HostConfigs: []db.HostConfig{
		{ID: 1, Type: db.HostConfigHost, Name: "mapped.example.test", SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: key}}},
		{ID: 2, Type: db.HostConfigURL, Name: "https://git.example.test/team/", SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: key}}},
		{ID: 3, Type: db.HostConfigURL, Name: "https://https.example.test/team/", SSHKey: db.AccessKey{Type: db.AccessKeyLoginPassword, LoginPassword: db.LoginPassword{Login: "mapping-user", Password: "mapping-password"}}},
	}}
	files, err := executor.prepareContainerCredentials(map[string][]string{"default": {}})
	require.NoError(t, err)
	entries := map[string][]byte{}
	for _, file := range files {
		entries[file.name] = file.data
	}
	config := string(entries["credentials/ssh-route.conf"])
	assert.Contains(t, config, "mapped.example.test")
	assert.Contains(t, config, "semaphore-mapping-2")
	assert.Contains(t, config, "HostName git.example.test")
	assert.NotEmpty(t, entries["credentials/ssh-key-mapping-1"])
	installation, err := ssh.InstallHostConfigs(1, executor.HostConfigs[2:], task_logger.NopLogger{})
	require.NoError(t, err)
	defer installation.Destroy()
	assert.Contains(t, installation.GitConfigParameters(), "https://mapping-user:mapping-password@https.example.test/team/")
}

func TestPrepareContainerTaskCarriesMappingsWithoutRunnerPaths(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "hcc")
	require.NoError(t, err)
	previousConfig := util.Config
	util.Config = &util.ConfigType{
		TmpPath: root,
		Ssh:     &util.SshConfig{StrictHostKeyChecking: util.SshStrictHostKeyCheckingAcceptNew},
		Process: &util.ConfigProcess{},
	}
	t.Cleanup(func() {
		util.Config = previousConfig
		require.NoError(t, os.RemoveAll(root))
	})

	repositoryPath := filepath.Join(root, "repository")
	require.NoError(t, os.MkdirAll(repositoryPath, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(repositoryPath, "site.yml"), []byte("---\n- hosts: all\n  gather_facts: false\n"), 0o600))
	arguments := `["--ssh-common-args","-o ProxyJump=bastion.example.test"]`
	privateKey, _ := repositoryAgentTestPrivateKey(t)

	newExecutor := func(hostConfigs []db.HostConfig) *LocalExecutor {
		return &LocalExecutor{
			Task:         db.Task{ID: 7},
			Template:     db.Template{ID: 3, ProjectID: 2, App: db.AppAnsible, Playbook: "site.yml", Arguments: &arguments},
			Repository:   db.Repository{ID: 4, ProjectID: 2, GitURL: repositoryPath},
			Inventory:    db.Inventory{ID: 5, ProjectID: 2, Type: db.InventoryStatic, Inventory: "localhost ansible_connection=local"},
			HostConfigs:  hostConfigs,
			KeyInstaller: ssh.KeyInstaller{},
			Logger:       task_logger.NopLogger{},
		}
	}

	t.Run("mappings are materialized without runner paths", func(t *testing.T) {
		executor := newExecutor([]db.HostConfig{
			{ID: 1, Type: db.HostConfigHost, Name: "mapped.example.test", SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: privateKey}}},
			{ID: 2, Type: db.HostConfigURL, Name: "https://git.example.test/team/", SSHKey: db.AccessKey{Type: db.AccessKeySSH, SshKey: db.SshKey{PrivateKey: privateKey}}},
			{ID: 3, Type: db.HostConfigURL, Name: "https://password.example.test/team/", SSHKey: db.AccessKey{Type: db.AccessKeyLoginPassword, LoginPassword: db.LoginPassword{Login: "token", Password: "mapping-password"}}},
		})
		plan, err := executor.PrepareContainerTask("", nil, "")
		require.NoError(t, err)
		t.Cleanup(executor.Cleanup)
		entries := readTaskBundle(t, plan.Bundle)
		require.NoError(t, plan.Bundle.Close())

		route := string(entries["credentials/ssh-route.conf"])
		environment := string(entries["credentials/environment.sh"])
		runScript := string(entries["run.sh"])
		assert.Contains(t, route, "mapped.example.test")
		assert.Contains(t, route, "semaphore-mapping-2")
		assert.NotEmpty(t, entries["credentials/ssh-key-mapping-1"])
		assert.Contains(t, environment, "GIT_CONFIG_PARAMETERS")
		assert.Contains(t, environment, "mapping-password")
		assert.NotContains(t, route, root)
		assert.NotContains(t, environment, root)
		assert.NotContains(t, runScript, root)
		assert.Contains(t, runScript, "'--ssh-common-args' '-o ProxyJump=bastion.example.test'")
		assert.NotContains(t, runScript, "ssh-config-")
	})

	t.Run("no mappings preserves authored SSH options", func(t *testing.T) {
		executor := newExecutor(nil)
		plan, err := executor.PrepareContainerTask("", nil, "")
		require.NoError(t, err)
		t.Cleanup(executor.Cleanup)
		entries := readTaskBundle(t, plan.Bundle)
		require.NoError(t, plan.Bundle.Close())

		assert.Contains(t, string(entries["run.sh"]), "'--ssh-common-args' '-o ProxyJump=bastion.example.test'")
		assert.NotContains(t, string(entries["credentials/environment.sh"]), "GIT_CONFIG_PARAMETERS")
	})
}

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
