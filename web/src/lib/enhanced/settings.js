import { findCapabilityDecision } from '@/lib/capabilities';

const enhancedComputed = {
  deploymentWindowsDecision() {
    const decision = findCapabilityDecision(this.systemInfo, 'deployment_windows');
    return decision?.access?.includes('read') ? decision : null;
  },
};

export default enhancedComputed;
