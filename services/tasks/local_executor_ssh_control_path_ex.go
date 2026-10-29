package tasks

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/semaphoreui/semaphore/util"
)

// prepareTaskSSHControlPath gives every task a private directory for SSH
// master sockets. Ansible's default ~/.ansible/cp with ControlPersist is
// shared by all tasks of the runner user, so a later task to the same
// host, user and port would reuse a master connection that another task
// authenticated with its own key, password or Infisical PAM session.
func (t *LocalExecutor) prepareTaskSSHControlPath() error {
	if t.taskSSHControlPathDir != "" {
		return nil
	}
	directory, err := os.MkdirTemp(taskSSHControlPathParent(), "sxcp")
	if err != nil {
		return fmt.Errorf("creating task SSH control path directory: %w", err)
	}
	// With process.user, Ansible runs as another user and must own the
	// directory to create master sockets in it. A hand-built configuration
	// (a test, a command) has no process section.
	if util.Config != nil && util.Config.Process != nil {
		err = util.ChownDir(directory)
	}
	if err != nil {
		_ = os.RemoveAll(directory)
		return fmt.Errorf("securing task SSH control path directory: %w", err)
	}
	t.taskSSHControlPathDir = directory
	return nil
}

func (t *LocalExecutor) removeTaskSSHControlPath() {
	if t.taskSSHControlPathDir != "" {
		_ = os.RemoveAll(t.taskSSHControlPathDir)
	}
	t.taskSSHControlPathDir = ""
}

// taskSSHControlPathParent keeps master socket paths short: OpenSSH rejects
// a ControlPath of 104 bytes or more, and %C alone adds 40. The project
// temporary directory can be arbitrarily deep, so the private directory is
// created under /tmp where it exists. os.MkdirTemp creates it with 0700.
func taskSSHControlPathParent() string {
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

// taskSSHControlPathEnvironment points Ansible's SSH multiplexing at the task
// directory. The values replace inherited ones on purpose: they are appended
// after template and runner environment values.
func (t *LocalExecutor) taskSSHControlPathEnvironment() []string {
	if t.taskSSHControlPathDir == "" {
		return nil
	}
	return []string{
		"ANSIBLE_SSH_CONTROL_PATH_DIR=" + t.taskSSHControlPathDir,
		"ANSIBLE_SSH_CONTROL_PATH=%(directory)s/%%C",
	}
}

// taskSSHControlPathWrapperOptions forces the task ControlPath for every ssh
// started through the routing wrapper, ahead of any inherited or caller value.
func (t *LocalExecutor) taskSSHControlPathWrapperOptions() []string {
	if t.taskSSHControlPathDir == "" {
		return nil
	}
	return []string{"-o", "ControlPath=" + filepath.Join(t.taskSSHControlPathDir, "%C")}
}

// isTaskSSHControlPathEnvironment identifies the runner-host control path
// values, which mean nothing inside a container: each container task has its
// own file system, so Ansible's default path there is already task-private.
func (t *LocalExecutor) isTaskSSHControlPathEnvironment(name, value string) bool {
	if t.taskSSHControlPathDir == "" {
		return false
	}
	for _, generated := range t.taskSSHControlPathEnvironment() {
		if generated == name+"="+value {
			return true
		}
	}
	return false
}
