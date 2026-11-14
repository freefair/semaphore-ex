import { findCapabilityDecision } from '@/lib/capabilities';
import { USER_PERMISSIONS } from '@/lib/constants';

const enhancedComputed = {
  canManageArtifactRetention() {
    // Retention belongs to the project's resources: the historic boundary.
    return this.projectCan(USER_PERMISSIONS.manageProjectResources);
  },
  deploymentWindowsDecision() {
    const decision = findCapabilityDecision(this.systemInfo, 'deployment_windows');
    return decision?.access?.includes('read') ? decision : null;
  },
  policyGuardrailsDecision() {
    const decision = findCapabilityDecision(this.systemInfo, 'policy_guardrails');
    return decision?.access?.some((access) => access === 'read' || access === 'write')
      ? decision : null;
  },
  canManagePolicyGuardrails() {
    return this.projectCan(USER_PERMISSIONS.managePolicyGuardrails);
  },
  canRollbackPolicyGuardrails() {
    return this.projectCan(USER_PERMISSIONS.rollbackPolicyGuardrails);
  },
};

export default enhancedComputed;
