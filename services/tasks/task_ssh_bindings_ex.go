package tasks

import (
	"errors"
	"fmt"

	"github.com/semaphoreui/semaphore/db"
)

// resolveTaskSSHKeyBindings resolves the immutable, value-free selection in
// the template owner's project. The key material is deliberately fetched only
// when a TaskRunner is dispatched.
func (p *TaskPool) resolveTaskSSHKeyBindings(template db.Template, task db.Task) (db.SSHKeyBindings, int, error) {
	ownerProjectID := template.ProjectID
	if ownerProjectID <= 0 {
		return nil, 0, errors.New("task SSH key owner project is invalid")
	}
	// A cross-project grant authorizes the published template definition, not
	// arbitrary access-key selection from the owner's key store. Only the
	// owner-authored template/default/always selection may cross that boundary.
	if task.ProjectID > 0 && task.ProjectID != ownerProjectID && task.SSHKeys != nil {
		return nil, 0, errors.New("cross-project task SSH key overrides are not authorized")
	}
	project, err := p.store.GetProject(ownerProjectID)
	if err != nil {
		return nil, 0, err
	}
	bindings, err := db.ResolveTaskSSHKeys(project.DefaultSSHKeys, project.AlwaysSSHKeys, template.SSHKeys, task.SSHKeys)
	if err != nil {
		return nil, 0, err
	}
	if err = p.validateTaskSSHKeyBindings(ownerProjectID, bindings); err != nil {
		return nil, 0, err
	}
	return bindings, ownerProjectID, nil
}

func (p *TaskPool) validateTaskSSHKeyBindings(projectID int, bindings db.SSHKeyBindings) error {
	if err := db.ValidateSSHKeyBindings(bindings); err != nil {
		return err
	}
	for _, binding := range bindings {
		key, err := p.store.GetAccessKey(projectID, binding.AccessKeyID)
		if err != nil || key.ProjectID == nil || *key.ProjectID != projectID || key.Type != db.AccessKeySSH {
			return fmt.Errorf("task SSH key %d is unavailable", binding.AccessKeyID)
		}
	}
	return nil
}

// hydrateResolvedTaskSSHKeys validates the frozen descriptors against the
// current key store and resolves their material through the established
// encryption service before the local executor receives the private task data.
func (t *TaskRunner) hydrateResolvedTaskSSHKeys(bindings db.SSHKeyBindings, projectID int) error {
	if projectID <= 0 {
		return errors.New("task SSH key owner project is invalid")
	}
	if err := t.pool.validateTaskSSHKeyBindings(projectID, bindings); err != nil {
		return err
	}
	t.ResolvedSSHKeys = make([]db.ResolvedTaskSSHKey, 0, len(bindings))
	for _, binding := range bindings {
		key, err := t.pool.store.GetAccessKey(projectID, binding.AccessKeyID)
		if err != nil || key.ProjectID == nil || *key.ProjectID != projectID || key.Type != db.AccessKeySSH {
			return fmt.Errorf("task SSH key %d is unavailable", binding.AccessKeyID)
		}
		if err = t.pool.encryptionService.DeserializeSecret(&key); err != nil {
			return fmt.Errorf("task SSH key %d cannot be resolved", binding.AccessKeyID)
		}
		t.ResolvedSSHKeys = append(t.ResolvedSSHKeys, db.ResolvedTaskSSHKey{Binding: binding, Key: key})
	}
	t.Task.ResolvedSSHKeys = append([]db.ResolvedTaskSSHKey(nil), t.ResolvedSSHKeys...)
	return nil
}
