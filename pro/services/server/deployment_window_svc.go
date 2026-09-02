package server

import "github.com/semaphoreui/semaphore/pro_interfaces"

// NewDeploymentWindowAdmissionService is unavailable in Community. Enhanced
// replaces this package with the implementation that owns the policy store.
func NewDeploymentWindowAdmissionService(pro_interfaces.DeploymentWindowPolicyRepository) pro_interfaces.DeploymentWindowAdmissionService {
	return nil
}

// NewDeploymentWindowGovernanceService is intentionally unavailable in
// Community. The Enhanced module provides the settings use-case boundary.
func NewDeploymentWindowGovernanceService(pro_interfaces.DeploymentWindowGovernanceRepository) pro_interfaces.DeploymentWindowGovernanceServiceFacade {
	return nil
}
