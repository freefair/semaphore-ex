package server

import "github.com/semaphoreui/semaphore/pro_interfaces"

// NewDeploymentWindowAdmissionService is unavailable in Community. Enhanced
// replaces this package with the implementation that owns the policy store.
func NewDeploymentWindowAdmissionService(pro_interfaces.DeploymentWindowPolicyRepository) pro_interfaces.DeploymentWindowAdmissionService {
	return nil
}
