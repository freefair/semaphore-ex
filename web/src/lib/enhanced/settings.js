import { findCapabilityDecision } from '@/lib/capabilities';
import { USER_PERMISSIONS } from '@/lib/constants';

const enhancedComputed = {
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
    return Boolean(this.isAdmin
        || ((this.userPermissions || 0) & USER_PERMISSIONS.managePolicyGuardrails));
  },
  canRollbackPolicyGuardrails() {
    return Boolean(this.isAdmin
        || ((this.userPermissions || 0) & USER_PERMISSIONS.rollbackPolicyGuardrails));
  },
};

export default enhancedComputed;
