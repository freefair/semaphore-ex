package server

import "github.com/semaphoreui/semaphore/pro_interfaces"

// NewPolicyGuardrailAdmissionService is unavailable in Community. Enhanced
// owns the compiler-backed final policy admission boundary.
func NewPolicyGuardrailAdmissionService(pro_interfaces.PolicyGuardrailAdmissionRepository) pro_interfaces.PolicyGuardrailAdmissionService {
	return nil
}

// NewPolicyGuardrailGovernanceService is unavailable in Community. Enhanced
// provides the draft, revision, and provenance settings use-case boundary.
func NewPolicyGuardrailGovernanceService(pro_interfaces.PolicyGuardrailGovernanceRepository) pro_interfaces.PolicyGuardrailGovernanceServiceFacade {
	return nil
}
