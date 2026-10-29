package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/infisicalpam"
	"github.com/semaphoreui/semaphore/services/server"
	"github.com/semaphoreui/semaphore/util"
)

const infisicalPAMConnectSubcommand = "infisical-pam-connect"

// taskInfisicalPAM is the executor's Infisical PAM state for one task.
type taskInfisicalPAM struct {
	// connectExecutable overrides the binary ProxyCommand runs; empty means
	// the running Semaphore executable.
	connectExecutable string
	// disabledReason explains why routes are not applied, e.g. container
	// execution, where the broker socket is unreachable.
	disabledReason string
	brokers        []*infisicalpam.Broker
}

func (t *LocalExecutor) infisicalPAMActive() bool {
	if t.infisicalPAM.disabledReason != "" {
		return false
	}
	for _, config := range t.Task.InfisicalPAM {
		if len(config.Routes) > 0 {
			return true
		}
	}
	return false
}

// disableInfisicalPAM keeps routes out of the task SSH configuration and says
// so once in the task log.
func (t *LocalExecutor) disableInfisicalPAM(reason string) {
	if t.infisicalPAM.disabledReason != "" {
		return
	}
	hadRoutes := t.infisicalPAMActive()
	t.infisicalPAM.disabledReason = reason
	if hadRoutes {
		t.Log("Infisical PAM routes are not applied: " + reason)
	}
}

func (t *LocalExecutor) infisicalPAMHosts() []string {
	if !t.infisicalPAMActive() {
		return nil
	}
	hosts := make([]string, 0)
	for _, config := range t.Task.InfisicalPAM {
		for _, route := range config.Routes {
			hosts = append(hosts, strings.ToLower(route.Host))
		}
	}
	sort.Strings(hosts)
	return hosts
}

// rejectInfisicalPAMConflicts refuses a host that is also routed to a task SSH
// key or a host mapping: two credentials for one host would make the
// effective one depend on ssh_config ordering.
func (t *LocalExecutor) rejectInfisicalPAMConflicts(taskHosts []string) error {
	routed := map[string]bool{}
	for _, host := range taskHosts {
		routed[strings.ToLower(host)] = true
	}
	for _, mapping := range t.HostConfigs {
		if mapping.Type == db.HostConfigHost {
			routed[strings.ToLower(mapping.Name)] = true
		}
	}
	for _, host := range t.infisicalPAMHosts() {
		if routed[host] {
			return fmt.Errorf("host %q is routed through Infisical PAM and also has an SSH credential", host)
		}
	}
	return nil
}

// startInfisicalPAMBrokers starts one broker per storage with sockets in the
// private directory and returns the ssh_config blocks that route its hosts.
func (t *LocalExecutor) startInfisicalPAMBrokers(directory string) (string, error) {
	if !t.infisicalPAMActive() {
		return "", nil
	}
	executable, err := t.infisicalPAMConnectExecutable()
	if err != nil {
		return "", err
	}
	// A PAM session must never be carried by, or lend its master connection
	// to, another task.
	if err = t.prepareTaskSSHControlPath(); err != nil {
		return "", err
	}
	var config strings.Builder
	for index, storage := range t.Task.InfisicalPAM {
		if len(storage.Routes) == 0 {
			continue
		}
		broker, startErr := t.startInfisicalPAMBroker(storage, filepath.Join(directory, fmt.Sprintf("%d.sock", index)))
		if startErr != nil {
			t.stopInfisicalPAMBrokers()
			return "", startErr
		}
		t.infisicalPAM.brokers = append(t.infisicalPAM.brokers, broker)
		for _, host := range broker.Hosts() {
			config.WriteString(infisicalPAMHostBlock(host, executable, broker.SocketPath()))
		}
		t.Log(fmt.Sprintf("Infisical PAM routes %d SSH host(s) through storage #%d", len(broker.Hosts()), storage.StorageID))
	}
	return config.String(), nil
}

func (t *LocalExecutor) startInfisicalPAMBroker(storage db.TaskInfisicalPAM, socket string) (*infisicalpam.Broker, error) {
	if err := server.ValidateTaskInfisicalPAM(storage); err != nil {
		return nil, fmt.Errorf("Infisical storage #%d is invalid: %w", storage.StorageID, err)
	}
	if storage.Credential.ID != storage.CredentialKeyID || storage.Credential.String == "" {
		return nil, fmt.Errorf("Infisical storage #%d client secret is unavailable", storage.StorageID)
	}
	duration, err := time.ParseDuration(storage.SessionDuration)
	if err != nil {
		return nil, fmt.Errorf("Infisical storage #%d session duration is invalid", storage.StorageID)
	}
	secret := []byte(storage.Credential.String)
	client, err := infisicalpam.NewClient(infisicalpam.Config{
		SiteURL:          storage.SiteURL,
		CACertificate:    []byte(storage.CACertificate),
		ClientID:         storage.ClientID,
		ClientSecret:     secret,
		OrganizationSlug: storage.OrganizationSlug,
	})
	if err != nil {
		return nil, fmt.Errorf("Infisical storage #%d: %w", storage.StorageID, err)
	}
	routes := make([]infisicalpam.Route, 0, len(storage.Routes))
	for _, route := range storage.Routes {
		routes = append(routes, infisicalpam.Route{Host: route.Host, AccountPath: route.Account})
	}
	broker, err := infisicalpam.StartBroker(infisicalpam.BrokerConfig{
		Client:     client,
		Routes:     routes,
		Duration:   duration,
		Reason:     fmt.Sprintf("Semaphore EX task #%d (project #%d)", t.Task.ID, t.Template.ProjectID),
		SocketPath: socket,
		Log:        t.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("starting Infisical PAM broker for storage #%d: %w", storage.StorageID, err)
	}
	return broker, nil
}

func (t *LocalExecutor) infisicalPAMConnectExecutable() (string, error) {
	if t.infisicalPAM.connectExecutable != "" {
		return t.infisicalPAM.connectExecutable, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating Semaphore executable for Infisical PAM: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}
	return executable, nil
}

// infisicalPAMHostBlock routes one validated host name through the broker.
// The host is written literally instead of using %n, so nothing from the
// inventory reaches the ProxyCommand shell. The gateway presents an
// ephemeral host key and does not verify the target's key, so host-key
// checking cannot apply on this route; the gateway accepts no client keys.
func infisicalPAMHostBlock(host, executable, socket string) string {
	command := posixQuote(executable) + " " + infisicalPAMConnectSubcommand +
		" --socket " + posixQuote(socket) + " --host " + host
	return "Host " + host + "\n" +
		"  ProxyCommand " + command + "\n" +
		"  StrictHostKeyChecking no\n" +
		"  UserKnownHostsFile /dev/null\n" +
		"  IdentitiesOnly yes\n" +
		"  IdentityAgent none\n" +
		"  ForwardAgent no\n"
}

func (t *LocalExecutor) stopInfisicalPAMBrokers() {
	for _, broker := range t.infisicalPAM.brokers {
		broker.Close()
	}
	t.infisicalPAM.brokers = nil
}

// prepareInfisicalPAMOnlyRouting writes a routing configuration for a task
// without task SSH keys. Other hosts keep the system and user SSH
// configuration through the shared includes.
func (t *LocalExecutor) prepareInfisicalPAMOnlyRouting() error {
	if !t.infisicalPAMActive() {
		return nil
	}
	if err := t.rejectInfisicalPAMConflicts(nil); err != nil {
		return err
	}
	parent := util.Config.GetProjectTmpDir(t.Template.ProjectID)
	if err := checkTmpDir(parent); err != nil {
		return fmt.Errorf("creating project temporary directory: %w", err)
	}
	directory, err := os.MkdirTemp(parent, "ipam-")
	if err != nil {
		return fmt.Errorf("creating Infisical PAM routing directory: %w", err)
	}
	if err = os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return fmt.Errorf("securing Infisical PAM routing directory: %w", err)
	}
	blocks, err := t.startInfisicalPAMBrokers(directory)
	if err != nil {
		_ = os.RemoveAll(directory)
		return err
	}
	contents := blocks
	if t.hostConfigInstallation != nil && t.hostConfigInstallation.SSHConfigPath() != "" {
		mapped, readErr := os.ReadFile(t.hostConfigInstallation.SSHConfigPath())
		if readErr != nil {
			t.stopInfisicalPAMBrokers()
			_ = os.RemoveAll(directory)
			return fmt.Errorf("reading host mapping SSH config: %w", readErr)
		}
		contents += string(mapped) + "\n"
	}
	if err = t.writeTaskSSHRoutingConfigFiles(directory, contents); err != nil {
		t.stopInfisicalPAMBrokers()
		_ = os.RemoveAll(directory)
		return err
	}
	return nil
}

// infisicalPAMSSHCommonArgs hands the routing config to sftp and scp, which
// start their own ssh binary instead of the wrapper.
func (t *LocalExecutor) infisicalPAMSSHCommonArgs() string {
	if !t.infisicalPAMActive() || t.taskSSHRoutingCommand == "" {
		return ""
	}
	config := filepath.Join(filepath.Dir(t.taskSSHRoutingCommand), "routing.conf")
	return "-F " + posixQuote(config)
}

// infisicalPAMOnlyEnvironment selects the routing wrapper for a task whose
// only SSH routing is Infisical PAM.
func (t *LocalExecutor) infisicalPAMOnlyEnvironment(existing []string) []string {
	if t.taskSSHRoutingCommand == "" || !t.infisicalPAMActive() {
		return nil
	}
	var environment []string
	if !environmentHasName(existing, "ANSIBLE_SSH_EXECUTABLE") {
		environment = append(environment, "ANSIBLE_SSH_EXECUTABLE="+t.taskSSHRoutingCommand)
	}
	return append(environment, "PATH="+filepath.Dir(t.taskSSHRoutingCommand)+":"+taskSSHRoutingBasePath(existing))
}
