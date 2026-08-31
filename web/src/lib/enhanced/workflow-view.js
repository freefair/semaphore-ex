import { findCapabilityDecision } from '@/lib/capabilities';

export const enhancedComputed = {
  canAdminister() {
    return Boolean(this.item?.effective_access?.administer);
  },
  triggerDecision() {
    return findCapabilityDecision(this.systemInfo, 'workflow_triggers');
  },
  triggersAvailable() {
    return Boolean(this.triggerDecision?.access?.includes('read'));
  },
};

export const enhancedMethods = {
  hasRunInputs(workflow) {
    return (workflow.parameters || []).length > 0
        || (workflow.nodes || []).some((node) => {
          const policy = node.override_policy || {};
          return (policy.inventory_ids || []).length
            || (policy.environment_ids || []).length
            || policy.allow_arguments
            || policy.allow_branch;
        });
  },
};
