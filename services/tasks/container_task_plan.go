package tasks

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	"github.com/semaphoreui/semaphore/util"
	sshcrypto "golang.org/x/crypto/ssh"
)

const (
	containerBundlePath    = "/semaphore/bundle"
	containerWorkspacePath = "/workspace"
)

// ContainerTaskStage identifies one fixed entry point in the read-only task
// bundle. The Docker API sees only this bounded stage selector; task arguments
// and environment values remain inside the task-scoped bundle.
type ContainerTaskStage string

const (
	ContainerTaskStageBootstrap ContainerTaskStage = "bootstrap"
	ContainerTaskStageRun       ContainerTaskStage = "run"
	ContainerTaskStagePlan      ContainerTaskStage = "plan"
	ContainerTaskStageApply     ContainerTaskStage = "apply"
)

// ContainerTerraformPlan tells a container executor how to continue after a
// successful Terraform plan. Exit code 0 means no changes and exit code 2 means
// changes because the generated plan command always uses -detailed-exitcode.
type ContainerTerraformPlan struct {
	PlanOnly    bool
	AutoApprove bool
}

// ContainerTaskPlan is the runner-to-container boundary. Bundle is a tar stream
// whose root is mounted read-only by the executor; Command returns a fixed,
// non-secret argv for each supported lifecycle stage.
type ContainerTaskPlan struct {
	App       db.TemplateApp
	Bundle    io.ReadCloser
	Terraform ContainerTerraformPlan
}

// Command returns the only command an executor needs to expose through the
// container API. Secrets and user-controlled arguments are intentionally absent.
func (p ContainerTaskPlan) Command(stage ContainerTaskStage) []string {
	return []string{"/bin/sh", path.Join(containerBundlePath, "run.sh"), string(stage)}
}

// PrepareContainerTask materializes the same approved checkout, inventory and
// credentials as local execution, but leaves dependency installation and task
// commands to the isolated container.
func (t *LocalExecutor) PrepareContainerTask(username string, incomingVersion *string, alias string) (*ContainerTaskPlan, error) {
	if err := t.installHostConfigs(); err != nil {
		return nil, err
	}
	if err := t.prepare(username, incomingVersion, alias, false); err != nil {
		t.Cleanup()
		return nil, err
	}
	if params := t.hostConfigInstallation.GitConfigParameters(); params != "" {
		t.preparedEnv = append(t.preparedEnv, "GIT_CONFIG_PARAMETERS="+params)
	}

	plan, err := t.ContainerTaskPlan()
	if err != nil {
		t.Cleanup()
		return nil, err
	}
	return plan, nil
}

// ContainerTaskPlan builds a single-use tar stream from already prepared task
// state. Callers normally use PrepareContainerTask; this separate method keeps
// bundle construction independently testable.
func (t *LocalExecutor) ContainerTaskPlan() (*ContainerTaskPlan, error) {
	if !t.prepared {
		return nil, fmt.Errorf("container task plan requires a prepared executor")
	}

	repositoryRoot := t.Repository.GetFullPath(t.Template.ID)
	if info, err := os.Stat(repositoryRoot); err != nil {
		return nil, fmt.Errorf("checking prepared repository: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("prepared repository %q is not a directory", repositoryRoot)
	}

	args := cloneStageArgs(t.preparedArgsMap)
	if generatedSSHCommonArgs := t.generatedContainerSSHCommonArgs(); generatedSSHCommonArgs != "" {
		for stage, stageArgs := range args {
			args[stage] = removeContainerArgWithValue(stageArgs, "--ssh-common-args", generatedSSHCommonArgs)
		}
	}
	extraFiles, err := t.prepareContainerCredentials(args)
	if err != nil {
		return nil, err
	}

	environment, err := t.containerEnvironment()
	if err != nil {
		return nil, err
	}
	runScript, terraformPlan, err := t.containerRunScript(args)
	if err != nil {
		return nil, err
	}
	environmentScript := renderContainerEnvironment(environment)

	reader, writer := io.Pipe()
	go func() {
		err := t.writeContainerBundle(writer, repositoryRoot, environmentScript, runScript, extraFiles)
		_ = writer.CloseWithError(err)
	}()

	return &ContainerTaskPlan{
		App:       t.Template.App,
		Bundle:    reader,
		Terraform: terraformPlan,
	}, nil
}

type containerBundleFile struct {
	name string
	mode int64
	data []byte
}

func (t *LocalExecutor) writeContainerBundle(
	destination io.Writer,
	repositoryRoot string,
	environmentScript string,
	runScript string,
	extraFiles []containerBundleFile,
) error {
	archive := tar.NewWriter(destination)

	if err := addContainerTree(archive, repositoryRoot, "repository"); err != nil {
		return fmt.Errorf("archiving task repository: %w", err)
	}

	if t.Inventory.Type.IsStatic() {
		contents, err := os.ReadFile(t.tmpInventoryFullPath())
		if err != nil {
			return fmt.Errorf("reading prepared inventory: %w", err)
		}
		if err := addContainerFile(archive, "inventory/hosts", 0o444, contents); err != nil {
			return err
		}
	} else if t.Inventory.Type == db.InventoryFile && t.Inventory.RepositoryID != nil && t.Inventory.Repository != nil {
		if err := addContainerTree(archive, t.tmpInventoryFullPath(), "inventory"); err != nil {
			return fmt.Errorf("archiving inventory repository: %w", err)
		}
	}

	callbackRoot := path.Join(t.Repository.GetInternalPath(t.Template.ID), "callbacks")
	if info, err := os.Stat(callbackRoot); err == nil && info.IsDir() {
		if err := addContainerTree(archive, callbackRoot, "internal/callbacks"); err != nil {
			return fmt.Errorf("archiving task callbacks: %w", err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("checking task callbacks: %w", err)
	}

	if err := addContainerDirectory(archive, "credentials", 0o700); err != nil {
		return err
	}
	files := append([]containerBundleFile{
		{name: "credentials/environment.sh", mode: 0o600, data: []byte(environmentScript)},
		{name: "run.sh", mode: 0o500, data: []byte(runScript)},
		{name: "inventory-resolver.py", mode: 0o500, data: []byte(db_lib.InventoryResolverScript())},
	}, extraFiles...)
	for _, file := range files {
		if err := addContainerFile(archive, file.name, file.mode, file.data); err != nil {
			return err
		}
	}
	return archive.Close()
}

func addContainerTree(archive *tar.Writer, sourceRoot string, archiveRoot string) error {
	return filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if sourcePath != sourceRoot && entry.Name() == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		name := path.Clean(path.Join(archiveRoot, filepath.ToSlash(relative)))
		if name == "." || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") {
			return fmt.Errorf("unsafe archive path %q", name)
		}

		info, err := os.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsupported symbolic link %q in container task bundle", relative)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported special file %q in container task bundle", relative)
		}
		if info.Mode().IsRegular() && containerFileHasMultipleLinks(info) {
			return fmt.Errorf("unsupported hard link %q in container task bundle", relative)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		header.Uid = 65534
		header.Gid = 0
		header.Uname = ""
		header.Gname = ""
		header.ModTime = time.Unix(0, 0).UTC()
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		if info.IsDir() {
			header.Mode = 0o555
		} else if info.Mode().IsRegular() {
			header.Mode = int64(info.Mode().Perm() & 0o555)
			if header.Mode&0o444 == 0 {
				header.Mode |= 0o444
			}
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(sourcePath)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(archive, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func containerFileHasMultipleLinks(info fs.FileInfo) bool {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return false
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	links := value.FieldByName("Nlink")
	if !links.IsValid() {
		return false
	}
	switch links.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return links.Uint() > 1
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return links.Int() > 1
	default:
		return false
	}
}

func addContainerDirectory(archive *tar.Writer, name string, mode int64) error {
	cleanName := path.Clean(name)
	if cleanName == "." || path.IsAbs(cleanName) || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	return archive.WriteHeader(&tar.Header{
		Name:     cleanName,
		Mode:     mode,
		Uid:      65534,
		Gid:      0,
		Typeflag: tar.TypeDir,
		ModTime:  time.Unix(0, 0).UTC(),
	})
}

func addContainerFile(archive *tar.Writer, name string, mode int64, data []byte) error {
	cleanName := path.Clean(name)
	if cleanName == "." || path.IsAbs(cleanName) || strings.HasPrefix(cleanName, "../") || strings.Contains(cleanName, "/../") {
		return fmt.Errorf("unsafe archive path %q", name)
	}
	header := &tar.Header{
		Name:     cleanName,
		Mode:     mode,
		Size:     int64(len(data)),
		Uid:      65534,
		Gid:      0,
		Typeflag: tar.TypeReg,
		ModTime:  time.Unix(0, 0).UTC(),
	}
	if err := archive.WriteHeader(header); err != nil {
		return err
	}
	_, err := archive.Write(data)
	return err
}

func cloneStageArgs(source map[string][]string) map[string][]string {
	result := make(map[string][]string, len(source))
	for stage, args := range source {
		result[stage] = slices.Clone(args)
	}
	return result
}

var containerEnvironmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (t *LocalExecutor) containerEnvironment() ([]string, error) {
	result := make([]string, 0, len(t.preparedEnv))
	for _, pair := range t.preparedEnv {
		if strings.ContainsRune(pair, '\x00') {
			return nil, fmt.Errorf("container environment contains NUL")
		}
		name, value, ok := strings.Cut(pair, "=")
		if !ok || !containerEnvironmentName.MatchString(name) {
			return nil, fmt.Errorf("invalid container environment variable %q", name)
		}
		if name == "SSH_AUTH_SOCK" ||
			(name == "GIT_SSH_COMMAND" && t.isGeneratedTaskGitSSHCommand(value)) ||
			(name == "ANSIBLE_SSH_EXECUTABLE" && t.taskSSHRoutingCommand != "" && value == t.taskSSHRoutingCommand) {
			continue
		}
		value = t.rewriteContainerPath(value)
		result = append(result, name+"="+value)
	}
	return result, nil
}

func (t *LocalExecutor) isGeneratedTaskGitSSHCommand(value string) bool {
	if t.taskSSHAgent == nil {
		return false
	}
	if t.taskSSHRoutingCommand != "" && value == posixQuote(t.taskSSHRoutingCommand) {
		return true
	}
	identities := append(append([]string(nil), t.repositorySSHIdentityFiles...), t.inventorySSHIdentityFiles...)
	identities = append(identities, t.extraSSHIdentityFiles...)
	return len(identities) > 0 && value == ssh.TaskGitSSHCommand(t.taskSSHAgent.SocketFile, identities)
}

func renderContainerEnvironment(environment []string) string {
	var script strings.Builder
	script.WriteString("# Generated task-scoped environment.\n")
	for _, pair := range environment {
		name, value, _ := strings.Cut(pair, "=")
		fmt.Fprintf(&script, "export %s=%s\n", name, posixQuote(value))
	}
	return script.String()
}

func (t *LocalExecutor) rewriteContainerPath(value string) string {
	replacements := [][2]string{
		{path.Join(t.Repository.GetInternalPath(t.Template.ID), "callbacks"), path.Join(containerBundlePath, "internal/callbacks")},
		{t.Repository.GetFullPath(t.Template.ID), containerWorkspacePath},
	}
	if t.Inventory.RepositoryID != nil && t.Inventory.Repository != nil {
		replacements = append(replacements, [2]string{t.tmpInventoryFullPath(), path.Join(containerBundlePath, "inventory")})
	} else if t.Inventory.Type.IsStatic() {
		replacements = append(replacements, [2]string{t.tmpInventoryFullPath(), path.Join(containerBundlePath, "inventory/hosts")})
	}
	for _, replacement := range replacements {
		if replacement[0] != "" {
			value = strings.ReplaceAll(value, replacement[0], replacement[1])
		}
	}
	return value
}

func (t *LocalExecutor) prepareContainerCredentials(args map[string][]string) ([]containerBundleFile, error) {
	files := make([]containerBundleFile, 0)
	sshKeys := []struct {
		name          string
		key           db.AccessKey
		hosts         []string
		implicitHosts []string
		route         bool
	}{{"inventory", t.Inventory.SSHKey, nil, nil, true}, {"repository", t.Repository.SSHKey, nil, repositorySSHHosts(t.Repository.GitURL), true}}
	for _, resolved := range t.Task.ResolvedSSHKeys {
		sshKeys = append(sshKeys, struct {
			name          string
			key           db.AccessKey
			hosts         []string
			implicitHosts []string
			route         bool
		}{fmt.Sprintf("extra-%d", resolved.Binding.AccessKeyID), resolved.Key, resolved.Binding.Hosts, nil, true})
	}
	for _, mapping := range t.HostConfigs {
		if mapping.SSHKey.Type != db.AccessKeySSH {
			continue
		}
		hosts := []string{}
		if mapping.Type == db.HostConfigHost {
			hosts = append(hosts, mapping.Name)
		} else {
			hosts = append(hosts, mapping.SSHAlias())
		}
		sshKeys = append(sshKeys, struct {
			name          string
			key           db.AccessKey
			hosts         []string
			implicitHosts []string
			route         bool
		}{"mapping-" + fmt.Sprint(mapping.ID), mapping.SSHKey, hosts, nil, false})
	}
	selectors := make(map[string]string)
	keyOrder := make([]string, 0, len(sshKeys))
	routingIdentities := make([]ssh.RoutingIdentity, 0, len(sshKeys))
	for _, item := range sshKeys {
		if item.key.Type != db.AccessKeySSH {
			continue
		}
		identity, err := ssh.PublicIdentity(item.key)
		if err != nil {
			return nil, fmt.Errorf("deriving %s SSH public identity: %w", item.name, err)
		}
		selector, exists := selectors[identity]
		if !exists {
			selector = path.Join(containerBundlePath, "credentials", "ssh-key-"+item.name+".pub")
			selectors[identity] = selector
			publicKey, err := containerSSHPublicIdentity(item.name, item.key)
			if err != nil {
				return nil, err
			}
			files = append(files,
				containerBundleFile{name: "credentials/ssh-key-" + item.name, mode: 0o600, data: []byte(item.key.SshKey.PrivateKey)},
				containerBundleFile{name: "credentials/ssh-passphrase-" + item.name, mode: 0o600, data: []byte(item.key.SshKey.Passphrase)},
				containerBundleFile{name: "credentials/ssh-key-" + item.name + ".pub", mode: 0o600, data: publicKey},
			)
			keyOrder = append(keyOrder, path.Join(containerBundlePath, "credentials", "ssh-key-"+item.name))
		}
		if item.route {
			routingIdentities = append(routingIdentities, ssh.RoutingIdentity{PublicKey: identity, Selector: selector, Hosts: item.hosts, ImplicitHosts: item.implicitHosts})
		}
	}
	if len(routingIdentities) > 0 || len(selectors) > 0 {
		routing, err := ssh.BuildHostRouting(routingIdentities)
		if err != nil {
			return nil, fmt.Errorf("building container SSH host routing: %w", err)
		}
		files = append(files, containerBundleFile{name: "ssh-askpass.sh", mode: 0o500, data: []byte("#!/bin/sh\nset -eu\nexec cat \"${SEMAPHORE_SSH_PASSPHRASE_FILE:?}\"\n")})
		routeConfig := ""
		for _, mapping := range t.HostConfigs {
			if mapping.SSHKey.Type != db.AccessKeySSH {
				continue
			}
			identity, identityErr := ssh.PublicIdentity(mapping.SSHKey)
			if identityErr != nil {
				return nil, identityErr
			}
			selector := selectors[identity]
			if mapping.Type == db.HostConfigURL {
				parsed, parseErr := url.Parse(mapping.Name)
				if parseErr != nil {
					return nil, parseErr
				}
				routeConfig += fmt.Sprintf("Host %s\n  HostName %s\n  IdentityFile %s\n  IdentitiesOnly yes\n", mapping.SSHAlias(), parsed.Hostname(), selector)
			} else {
				routeConfig += fmt.Sprintf("Host %s\n  IdentityFile %s\n  IdentitiesOnly yes\n", mapping.Name, selector)
			}
		}
		routeConfig += routing.Config(path.Join(containerWorkspacePath, ".semaphore", "ssh-agent.sock"))
		files = append(files,
			containerBundleFile{name: "credentials/ssh-key-order", mode: 0o600, data: []byte(strings.Join(keyOrder, "\n") + "\n")},
			containerBundleFile{name: "credentials/ssh-route.conf", mode: 0o600, data: []byte(routeConfig + taskSSHConfigIncludes("~/.ssh/config", "/etc/ssh/ssh_config"))},
			containerBundleFile{name: "ssh-route.sh", mode: 0o500, data: []byte("#!/bin/sh\nset -eu\nexec \"${SEMAPHORE_SYSTEM_SSH:?}\" -F /semaphore/bundle/credentials/ssh-route.conf \"$@\"\n")},
			containerBundleFile{name: "ssh-bin/ssh", mode: 0o500, data: []byte("#!/bin/sh\nset -eu\nexec \"${SEMAPHORE_SYSTEM_SSH:?}\" -F /semaphore/bundle/credentials/ssh-route.conf \"$@\"\n")},
		)
	}

	if t.Template.App != db.AppAnsible {
		return files, nil
	}

	passwords := make(map[string]string)
	defaultArgs := args["default"]
	if t.Inventory.SSHKeyID != nil && t.Inventory.SSHKey.Type == db.AccessKeyLoginPassword {
		defaultArgs = removeContainerArg(defaultArgs, "--ask-pass")
		passwords["ansible_password"] = t.Inventory.SSHKey.LoginPassword.Password
	}
	if t.Inventory.BecomeKeyID != nil && t.Inventory.BecomeKey.Type == db.AccessKeyLoginPassword {
		defaultArgs = removeContainerArg(defaultArgs, "--ask-become-pass")
		passwords["ansible_become_password"] = t.Inventory.BecomeKey.LoginPassword.Password
	}
	if len(passwords) > 0 {
		contents, err := json.Marshal(passwords)
		if err != nil {
			return nil, fmt.Errorf("encoding container ansible credentials: %w", err)
		}
		files = append(files, containerBundleFile{name: "credentials/ansible-passwords.json", mode: 0o600, data: contents})
		defaultArgs = append(defaultArgs, "--extra-vars", "@"+path.Join(containerBundlePath, "credentials/ansible-passwords.json"))
	}

	for index, vault := range t.Template.Vaults {
		if vault.Type != db.TemplateVaultPassword || vault.Vault == nil {
			continue
		}
		name := "default"
		if vault.Name != nil {
			name = *vault.Name
		}
		filename := fmt.Sprintf("credentials/vault-%d", index)
		files = append(files, containerBundleFile{name: filename, mode: 0o600, data: []byte(vault.Vault.LoginPassword.Password)})
		defaultArgs = replaceContainerArg(defaultArgs, "--vault-id="+name+"@prompt", "--vault-id="+name+"@"+path.Join(containerBundlePath, filename))
	}
	for _, vault := range t.Template.Vaults {
		if vault.Type != db.TemplateVaultScript || vault.Script == nil {
			continue
		}
		cleanScript := path.Clean(*vault.Script)
		if cleanScript == "." || path.IsAbs(cleanScript) || strings.HasPrefix(cleanScript, "../") || strings.Contains(cleanScript, "/../") {
			return nil, fmt.Errorf("vault script must be relative to the task repository")
		}
		info, err := os.Lstat(filepath.Join(t.Repository.GetFullPath(t.Template.ID), filepath.FromSlash(cleanScript)))
		if err != nil {
			return nil, fmt.Errorf("checking vault script %q: %w", cleanScript, err)
		}
		if !info.Mode().IsRegular() || containerFileHasMultipleLinks(info) {
			return nil, fmt.Errorf("vault script %q is not a regular task file", cleanScript)
		}
		name := "default"
		if vault.Name != nil {
			name = *vault.Name
		}
		defaultArgs = replaceContainerArg(defaultArgs, "--vault-id="+name+"@"+*vault.Script, "--vault-id="+name+"@"+path.Join(containerWorkspacePath, cleanScript))
	}
	args["default"] = defaultArgs
	return files, nil
}

func containerSSHPublicIdentity(role string, key db.AccessKey) ([]byte, error) {
	var (
		privateKey any
		err        error
	)
	if key.SshKey.Passphrase == "" {
		privateKey, err = sshcrypto.ParseRawPrivateKey([]byte(key.SshKey.PrivateKey))
	} else {
		privateKey, err = sshcrypto.ParseRawPrivateKeyWithPassphrase([]byte(key.SshKey.PrivateKey), []byte(key.SshKey.Passphrase))
	}
	if err != nil {
		return nil, fmt.Errorf("parsing %s SSH public identity: %w", role, err)
	}
	signer, err := sshcrypto.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("creating %s SSH public identity: %w", role, err)
	}
	return sshcrypto.MarshalAuthorizedKey(signer.PublicKey()), nil
}

func removeContainerArg(args []string, target string) []string {
	result := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != target {
			result = append(result, arg)
		}
	}
	return result
}

// removeContainerArgWithValue removes only the generated runner option whose
// path cannot exist inside a container. User-authored SSH options remain task
// input and must keep their original meaning.
func removeContainerArgWithValue(args []string, target, value string) []string {
	result := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == target && index+1 < len(args) && args[index+1] == value {
			index++
			continue
		}
		result = append(result, args[index])
	}
	return result
}

func (t *LocalExecutor) generatedContainerSSHCommonArgs() string {
	if t.hostConfigInstallation == nil || t.hostConfigInstallation.SSHConfigPath() == "" {
		return ""
	}
	return fmt.Sprintf("-F %s", t.hostConfigInstallation.SSHConfigPath())
}

func replaceContainerArg(args []string, target string, replacement string) []string {
	result := slices.Clone(args)
	for index, arg := range result {
		if arg == target {
			result[index] = replacement
		}
	}
	return result
}

func (t *LocalExecutor) containerRunScript(args map[string][]string) (string, ContainerTerraformPlan, error) {
	var script strings.Builder
	script.WriteString("#!/bin/sh\nset -eu\n")
	script.WriteString("bundle_dir=/semaphore/bundle\nworkspace=/workspace\nhome_dir=/home/semaphore\n")
	script.WriteString("runner_path=${PATH-/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin}\n")
	script.WriteString("stage=${1-}\n")
	script.WriteString("load_environment() {\n  . \"${bundle_dir}/credentials/environment.sh\"\n  export HOME=\"${home_dir}\"\n  export PATH=\"${runner_path}\"\n  export PWD=\"${workspace}\"\n  unset SSH_ASKPASS SSH_ASKPASS_REQUIRE\n  if [ -e \"${workspace}/.semaphore/ssh-agent.sock\" ]; then export SSH_AUTH_SOCK=\"${workspace}/.semaphore/ssh-agent.sock\"; else unset SSH_AUTH_SOCK; fi\n  export ANSIBLE_HOST_KEY_CHECKING=False ANSIBLE_FORCE_COLOR=True ANSIBLE_REMOTE_TMP=/tmp/.ansible/tmp PYTHONUNBUFFERED=1\n  if [ -x \"${bundle_dir}/ssh-route.sh\" ]; then\n    semaphore_system_ssh=\"$(command -v ssh)\"\n    case \"${semaphore_system_ssh}\" in /*) ;; *) semaphore_system_ssh=\"$(cd \"$(dirname \"${semaphore_system_ssh}\")\" && pwd)/$(basename \"${semaphore_system_ssh}\")\" ;; esac\n    export SEMAPHORE_SYSTEM_SSH=\"${semaphore_system_ssh}\"\n    export PATH=\"${bundle_dir}/ssh-bin:${runner_path}\"\n    if [ -z \"${GIT_SSH_COMMAND+x}\" ]; then export GIT_SSH_COMMAND=\"${bundle_dir}/ssh-route.sh -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/workspace/.semaphore/known_hosts\"; fi\n    if [ -z \"${ANSIBLE_SSH_EXECUTABLE+x}\" ]; then export ANSIBLE_SSH_EXECUTABLE=\"${bundle_dir}/ssh-route.sh\"; fi\n  elif [ -z \"${GIT_SSH_COMMAND+x}\" ]; then\n    export GIT_SSH_COMMAND='ssh -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/workspace/.semaphore/known_hosts'\n  fi\n}\n")
	script.WriteString("start_ssh_agent() {\n  order_file=\"${bundle_dir}/credentials/ssh-key-order\"\n  [ -s \"${order_file}\" ] || return 0\n  mkdir -p \"${workspace}/.semaphore\"\n  eval \"$(ssh-agent -a \"${workspace}/.semaphore/ssh-agent.sock\" -s)\" >/dev/null\n  while IFS= read -r key; do\n    [ -s \"${key}\" ] || continue\n    key_name=\"${key#${bundle_dir}/credentials/ssh-key-}\"\n    passphrase=\"${bundle_dir}/credentials/ssh-passphrase-${key_name}\"\n    if [ -s \"${passphrase}\" ]; then\n      SEMAPHORE_SSH_PASSPHRASE_FILE=\"${passphrase}\" SSH_ASKPASS=\"${bundle_dir}/ssh-askpass.sh\" SSH_ASKPASS_REQUIRE=force DISPLAY=semaphore ssh-add \"${key}\" </dev/null >/dev/null\n    else\n      ssh-add \"${key}\" </dev/null >/dev/null\n    fi\n  done < \"${order_file}\"\n}\n")
	script.WriteString("bootstrap() {\n  mkdir -p \"${workspace}\" \"${home_dir}\"\n  cp -R \"${bundle_dir}/repository/.\" \"${workspace}/\"\n  load_environment\n  start_ssh_agent\n")

	terraformPlan := ContainerTerraformPlan{}
	switch {
	case t.Template.App == db.AppAnsible:
		script.WriteString(t.containerAnsibleBootstrap())
	case t.Template.App.IsTerraform():
		terraformBootstrap, plan, err := t.containerTerraformBootstrap(args)
		if err != nil {
			return "", ContainerTerraformPlan{}, err
		}
		terraformPlan = plan
		script.WriteString(terraformBootstrap)
	}
	script.WriteString("}\n")

	runCommand, err := t.containerRunCommand(args)
	if err != nil {
		return "", ContainerTerraformPlan{}, err
	}
	script.WriteString("run_task() {\n  load_environment\n  cd ")
	script.WriteString(posixQuote(t.containerWorkingDirectory()))
	script.WriteString("\n  exec ")
	script.WriteString(joinContainerCommand(runCommand))
	script.WriteString("\n}\n")

	if t.Template.App.IsTerraform() {
		planCommand, applyCommand := t.containerTerraformCommands(args)
		script.WriteString("plan_task() {\n  load_environment\n  cd ")
		script.WriteString(posixQuote(t.containerWorkingDirectory()))
		script.WriteString("\n  exec ")
		script.WriteString(joinContainerCommand(planCommand))
		script.WriteString("\n}\n")
		script.WriteString("apply_task() {\n  load_environment\n  cd ")
		script.WriteString(posixQuote(t.containerWorkingDirectory()))
		script.WriteString("\n  exec ")
		script.WriteString(joinContainerCommand(applyCommand))
		script.WriteString("\n}\n")
	}

	script.WriteString("case \"${stage}\" in\n  bootstrap) bootstrap ;;\n  run) run_task ;;\n")
	if t.Template.App.IsTerraform() {
		script.WriteString("  plan) plan_task ;;\n  apply) apply_task ;;\n")
	}
	script.WriteString("  *) echo \"unsupported task stage: ${stage}\" >&2; exit 64 ;;\nesac\n")
	return script.String(), terraformPlan, nil
}

func (t *LocalExecutor) containerAnsibleBootstrap() string {
	tplParams, _ := t.preparedTplParams.(*db.AnsibleTemplateParams)
	params, _ := t.preparedParams.(*db.AnsibleTaskParams)
	skip := tplParams != nil && tplParams.SkipGalaxyInstall
	if tplParams != nil && tplParams.AllowOverrideSkipGalaxyInstall && params != nil {
		skip = params.SkipGalaxyInstall
	}
	if skip {
		return "  echo 'Galaxy install step is skipped.'\n"
	}

	playbookDir := path.Dir(path.Join(containerWorkspacePath, strings.TrimPrefix(t.Template.Playbook, "/")))
	requirements := []struct {
		typeName string
		filename string
	}{
		{"collection", path.Join(playbookDir, "collections/requirements.yml")},
		{"collection", path.Join(playbookDir, "requirements.yml")},
		{"collection", path.Join(containerWorkspacePath, "collections/requirements.yml")},
		{"collection", path.Join(containerWorkspacePath, "requirements.yml")},
		{"role", path.Join(playbookDir, "roles/requirements.yml")},
		{"role", path.Join(playbookDir, "requirements.yml")},
		{"role", path.Join(containerWorkspacePath, "roles/requirements.yml")},
		{"role", path.Join(containerWorkspacePath, "requirements.yml")},
	}
	var script strings.Builder
	for _, requirement := range requirements {
		fmt.Fprintf(&script, "  if [ -f %s ]; then ansible-galaxy %s install -r %s --force; fi\n",
			posixQuote(requirement.filename), posixQuote(requirement.typeName), posixQuote(requirement.filename))
	}
	return script.String()
}

func (t *LocalExecutor) containerTerraformBootstrap(args map[string][]string) (string, ContainerTerraformPlan, error) {
	params, ok := t.preparedParams.(*db.TerraformTaskParams)
	if !ok || params == nil {
		return "", ContainerTerraformPlan{}, fmt.Errorf("terraform container plan requires task parameters")
	}
	tplParams, ok := t.preparedTplParams.(*db.TerraformTemplateParams)
	if !ok || tplParams == nil {
		return "", ContainerTerraformPlan{}, fmt.Errorf("terraform container plan requires template parameters")
	}

	binary, prefix := t.containerApplicationCommand()
	initArgs := []string{"init", "-lock=false", "-input=false"}
	if params.Upgrade {
		initArgs = append(initArgs, "-upgrade")
	}
	if params.Reconfigure {
		initArgs = append(initArgs, "-reconfigure")
	} else {
		initArgs = append(initArgs, "-migrate-state")
	}
	initArgs = append(initArgs, args["init"]...)
	initCommand := append(append([]string{binary}, prefix...), t.containerTerraformArgs(initArgs)...)

	workspace := "default"
	if t.Inventory.Inventory != "" {
		workspace = t.Inventory.Inventory
	}
	workspaceList := append(append([]string{binary}, prefix...), t.containerTerraformWorkspaceArgs([]string{"workspace", "list"})...)
	workspaceSelect := append(append([]string{binary}, prefix...), t.containerTerraformWorkspaceArgs([]string{"workspace", "select", "-or-create=true", workspace})...)

	var script strings.Builder
	workingDirectory := t.containerWorkingDirectory()
	fmt.Fprintf(&script, "  mkdir -p %s\n", posixQuote(workingDirectory))
	if tplParams.OverrideBackend {
		filename := tplParams.BackendFilename
		if filename == "" {
			filename = "backend.tf"
		}
		fmt.Fprintf(&script, "  printf '%%s\\n' 'terraform {' '  backend \"http\" {' '  }' '}' > %s\n", posixQuote(path.Join(workingDirectory, filename)))
	}
	fmt.Fprintf(&script, "  (cd %s && %s)\n", posixQuote(workingDirectory), joinContainerCommand(initCommand))
	fmt.Fprintf(&script, "  if (cd %s && %s >/dev/null 2>&1); then (cd %s && %s); fi\n",
		posixQuote(workingDirectory), joinContainerCommand(workspaceList), posixQuote(workingDirectory), joinContainerCommand(workspaceSelect))

	return script.String(), ContainerTerraformPlan{
		PlanOnly:    params.Plan,
		AutoApprove: tplParams.AutoApprove || tplParams.AllowAutoApprove && params.AutoApprove,
	}, nil
}

func (t *LocalExecutor) containerRunCommand(args map[string][]string) ([]string, error) {
	defaultArgs := slices.Clone(args["default"])
	switch t.Template.App {
	case db.AppAnsible:
		defaultArgs = t.rewriteContainerArgs(defaultArgs)
		mode := "run"
		if t.Task.IsInventoryRefresh() {
			mode = "refresh"
		}
		return append([]string{"python3", path.Join(containerBundlePath, "inventory-resolver.py"), mode}, defaultArgs...), nil
	case db.AppTerraform, db.AppTofu, db.AppTerragrunt:
		return nil, nil
	default:
		binary, prefix := t.containerApplicationCommand()
		defaultArgs = t.rewriteContainerArgs(defaultArgs)
		if len(defaultArgs) > 0 && defaultArgs[0] == t.Template.Playbook {
			defaultArgs[0] = path.Join(containerWorkspacePath, strings.TrimPrefix(defaultArgs[0], "/"))
		}
		return append(append([]string{binary}, prefix...), defaultArgs...), nil
	}
}

func (t *LocalExecutor) containerTerraformCommands(args map[string][]string) ([]string, []string) {
	binary, prefix := t.containerApplicationCommand()
	planArgs := args["plan"]
	if planArgs == nil {
		planArgs = args["default"]
	}
	applyArgs := args["apply"]
	if applyArgs == nil {
		applyArgs = args["default"]
	}
	planStage := []string{"plan", "-lock=false", "-detailed-exitcode", "-input=false"}
	planStage = append(planStage, t.rewriteContainerArgs(planArgs)...)
	applyStage := []string{"apply", "-auto-approve", "-lock=false", "-input=false"}
	applyStage = append(applyStage, t.rewriteContainerArgs(applyArgs)...)
	planCommand := append(append([]string{binary}, prefix...), t.containerTerraformArgs(planStage)...)
	applyCommand := append(append([]string{binary}, prefix...), t.containerTerraformArgs(applyStage)...)
	return planCommand, applyCommand
}

func (t *LocalExecutor) containerApplicationCommand() (string, []string) {
	binary := string(t.Template.App)
	switch t.Template.App {
	case db.AppBash:
		binary = "bash"
	case db.AppPython:
		binary = "python3"
	case db.AppPowerShell:
		binary = "powershell"
	}
	prefix := []string(nil)
	if app, ok := util.Config.Apps[string(t.Template.App)]; ok {
		if app.AppPath != "" {
			binary = app.AppPath
		}
		prefix = slices.Clone(app.AppArgs)
	}
	if t.Template.App == db.AppPowerShell && len(prefix) == 0 {
		prefix = []string{"-File"}
	}
	return binary, prefix
}

func (t *LocalExecutor) containerTerraformArgs(args []string) []string {
	result := slices.Clone(args)
	if t.Template.App == db.AppTerragrunt && !hasContainerTerraformPath(result) {
		result = append(result, "--tf-path=terraform")
	}
	return result
}

func (t *LocalExecutor) containerTerraformWorkspaceArgs(args []string) []string {
	if t.Template.App != db.AppTerragrunt {
		return args
	}
	return append([]string{"run", "--tf-path=terraform", "--"}, args...)
}

func hasContainerTerraformPath(args []string) bool {
	for _, arg := range args {
		if arg == "--tf-path" || strings.HasPrefix(arg, "--tf-path=") {
			return true
		}
	}
	return false
}

func (t *LocalExecutor) rewriteContainerArgs(args []string) []string {
	result := slices.Clone(args)
	for index, arg := range result {
		result[index] = t.rewriteContainerPath(arg)
	}
	if t.Template.App == db.AppAnsible {
		for index := 0; index+1 < len(result); index++ {
			if result[index] == "-i" {
				switch {
				case t.Inventory.Type.IsStatic():
					result[index+1] = path.Join(containerBundlePath, "inventory/hosts")
				case t.Inventory.RepositoryID != nil:
					result[index+1] = path.Join(containerBundlePath, "inventory", strings.TrimPrefix(t.Inventory.GetFilename(), "/"))
				}
			}
		}
	}
	return result
}

func (t *LocalExecutor) containerWorkingDirectory() string {
	if t.Template.App.IsTerraform() {
		return path.Join(containerWorkspacePath, strings.TrimPrefix(t.Template.Playbook, "/"))
	}
	return containerWorkspacePath
}

func joinContainerCommand(args []string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = posixQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func posixQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
