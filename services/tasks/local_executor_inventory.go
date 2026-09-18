package tasks

import (
	"fmt"
	"os"
	"path"
	"strconv"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/db_lib"
	"github.com/semaphoreui/semaphore/pkg/ssh"
	log "github.com/sirupsen/logrus"
	sshcrypto "golang.org/x/crypto/ssh"

	"github.com/semaphoreui/semaphore/util"
)

func (t *LocalExecutor) installInventory() (err error) {
	if t.Inventory.SSHKeyID != nil {
		if t.Inventory.SSHKey.Type == db.AccessKeySSH {
			// The task-scoped agent is created before requirements installation so
			// every subprocess can use the same inventory and repository key set.
			t.sshKeyInstallation.Login = t.Inventory.SSHKey.SshKey.Login
		} else {
			t.sshKeyInstallation, err = t.KeyInstaller.Install(t.Inventory.SSHKey, db.AccessKeyRoleAnsibleUser, t.Logger)
			if err != nil {
				return
			}
		}
	}

	if t.Inventory.BecomeKeyID != nil {
		t.becomeKeyInstallation, err = t.KeyInstaller.Install(t.Inventory.BecomeKey, db.AccessKeyRoleAnsibleBecomeUser, t.Logger)
		if err != nil {
			return
		}
	}

	switch t.Inventory.Type {
	case db.InventoryFile:
		err = t.cloneInventoryRepo(t.KeyInstaller)
	case db.InventoryStatic, db.InventoryStaticYaml:
		err = t.installStaticInventory()
	}

	return
}

func (t *LocalExecutor) startTaskSSHAgent() error {
	allKeys := []db.AccessKey{t.Inventory.SSHKey, t.Repository.SSHKey}
	for _, resolved := range t.Task.ResolvedSSHKeys {
		allKeys = append(allKeys, resolved.Key)
	}
	keys := ssh.AgentKeys(allKeys...)
	if len(keys) == 0 {
		return nil
	}
	agent, err := ssh.StartSSHAgentWithKeys(keys, &t.Template.ProjectID, t.Logger)
	if err != nil {
		return fmt.Errorf("starting task SSH agent: %w", err)
	}
	t.taskSSHAgent = &agent
	t.inventorySSHIdentityFiles, err = t.writeTaskSSHIdentityFiles("inventory", t.Inventory.SSHKey)
	if err != nil {
		_ = t.taskSSHAgent.Close()
		t.taskSSHAgent = nil
		return err
	}
	t.repositorySSHIdentityFiles, err = t.writeTaskSSHIdentityFiles("repository", t.Repository.SSHKey)
	if err != nil {
		t.removeTaskSSHIdentityFiles()
		_ = t.taskSSHAgent.Close()
		t.taskSSHAgent = nil
		return err
	}
	for _, resolved := range t.Task.ResolvedSSHKeys {
		selectors, selectorErr := t.writeTaskSSHIdentityFiles("task-"+strconv.Itoa(resolved.Binding.AccessKeyID), resolved.Key)
		if selectorErr != nil {
			t.removeTaskSSHIdentityFiles()
			_ = t.taskSSHAgent.Close()
			t.taskSSHAgent = nil
			return selectorErr
		}
		t.extraSSHIdentityFiles = append(t.extraSSHIdentityFiles, firstSelector(selectors))
	}
	if err = t.prepareTaskSSHRouting(); err != nil {
		t.removeTaskSSHIdentityFiles()
		_ = t.taskSSHAgent.Close()
		t.taskSSHAgent = nil
		return err
	}
	return nil
}

func (t *LocalExecutor) writeTaskSSHIdentityFiles(role string, key db.AccessKey) ([]string, error) {
	if key.Type != db.AccessKeySSH || t.taskSSHAgent == nil {
		return nil, nil
	}
	var privateKey any
	var err error
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
	filename := t.taskSSHAgent.SocketFile + "." + role + ".pub"
	if err := os.WriteFile(filename, sshcrypto.MarshalAuthorizedKey(signer.PublicKey()), 0o600); err != nil {
		return nil, fmt.Errorf("writing %s SSH public identity: %w", role, err)
	}
	t.taskSSHIdentityFiles = append(t.taskSSHIdentityFiles, filename)
	return []string{filename}, nil
}

func (t *LocalExecutor) removeTaskSSHIdentityFiles() {
	for _, filename := range t.taskSSHIdentityFiles {
		_ = os.Remove(filename)
	}
	t.taskSSHIdentityFiles = nil
	t.inventorySSHIdentityFiles = nil
	t.repositorySSHIdentityFiles = nil
	t.extraSSHIdentityFiles = nil
}

func (t *LocalExecutor) tmpInventoryFilename() string {
	if t.Inventory.Repository == nil {
		return "inventory_" + strconv.Itoa(t.Inventory.ID)
	}
	return t.Inventory.Repository.GetDirName(t.Template.ID) + "_inventory_" + strconv.Itoa(t.Inventory.ID)
}

func (t *LocalExecutor) tmpInventoryFullPath() string {
	if t.Inventory.Repository != nil && t.Inventory.Repository.GetType() == db.RepositoryLocal {
		return t.Inventory.Repository.GetGitURL(true)
	}
	pathname := path.Join(util.Config.GetProjectTmpDir(t.Template.ProjectID), t.tmpInventoryFilename())
	if t.Inventory.Type == db.InventoryStaticYaml {
		pathname += ".yml"
	}
	return pathname
}

func (t *LocalExecutor) cloneInventoryRepo(keyInstaller db_lib.AccessKeyInstaller) error {
	if t.Inventory.Repository == nil {
		return nil
	}

	if t.Inventory.Repository.GetType() == db.RepositoryLocal {
		return nil
	}

	t.Log("cloning inventory repository")

	repo := db_lib.GitRepository{
		Logger:     t.Logger,
		TmpDirName: t.tmpInventoryFilename(),
		Repository: *t.Inventory.Repository,
		Client:     db_lib.CreateDefaultGitClient(keyInstaller),
	}

	// Parallel tasks of the same template share this inventory directory —
	// serialize the pull/remove/clone sequence, same as the main repo.
	unlock := t.RepoLock.Lock(repo.GetFullPath())
	defer unlock()

	// Try to pull the repo before trying to clone it
	if repo.CanBePulled() {
		err := repo.Pull()
		if err == nil {
			return nil
		}
	}

	err := os.RemoveAll(repo.GetFullPath())
	if err != nil {
		return err
	}

	return repo.Clone()
}

func (t *LocalExecutor) installStaticInventory() error {
	t.Log("installing static inventory")

	fullPath := t.tmpInventoryFullPath()

	// create inventory file
	return os.WriteFile(fullPath, []byte(t.Inventory.Inventory), 0664)
}

func (t *LocalExecutor) destroyInventoryFile() {
	if !t.Inventory.Type.IsStatic() {
		return
	}

	fullPath := t.tmpInventoryFullPath()
	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return
		}

		log.WithError(err).WithFields(log.Fields{
			"context": "task_running",
			"task_id": t.Task.ID,
		}).Warn("failed to remove inventory file")
	}
}

func (t *LocalExecutor) destroyKeys() {
	t.removeTaskSSHRoutingFiles()
	t.removeTaskSSHIdentityFiles()
	if t.taskSSHAgent != nil {
		if err := t.taskSSHAgent.Close(); err != nil {
			t.Log("Can't destroy task SSH agent, error: " + err.Error())
		}
		t.taskSSHAgent = nil
	}
	err := t.sshKeyInstallation.Destroy()
	if err != nil {
		t.Log("Can't destroy inventory user key, error: " + err.Error())
	}

	err = t.becomeKeyInstallation.Destroy()
	if err != nil {
		t.Log("Can't destroy inventory become user key, error: " + err.Error())
	}

	for _, vault := range t.vaultFileInstallations {
		err = vault.Destroy()
		if err != nil {
			t.Log("Can't destroy inventory vault password file, error: " + err.Error())
		}
	}
}
