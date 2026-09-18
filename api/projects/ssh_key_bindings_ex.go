package projects

import (
	"fmt"

	"github.com/semaphoreui/semaphore/db"
	"github.com/semaphoreui/semaphore/pkg/common_errors"
)

// validateSSHKeyBindingsForProject restricts bindings to SSH keys owned by the
// project that owns the configuration. A numeric key ID never confers access
// to another project or a global credential.
func validateSSHKeyBindingsForProject(store db.Store, projectID int, bindings db.SSHKeyBindings) error {
	if err := db.ValidateSSHKeyBindings(bindings); err != nil {
		return common_errors.NewValidationError(err.Error())
	}
	for _, binding := range bindings {
		key, err := store.GetAccessKey(projectID, binding.AccessKeyID)
		if err != nil || key.ProjectID == nil || *key.ProjectID != projectID || key.Type != db.AccessKeySSH {
			return common_errors.NewValidationError(fmt.Sprintf("SSH key %d is unavailable in this project", binding.AccessKeyID))
		}
	}
	return nil
}
