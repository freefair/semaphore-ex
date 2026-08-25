import { capabilityStateColor, findCapabilityDecision } from '@/lib/capabilities';

export const enhancedComputed = {
  lifecycleDecision() {
    return findCapabilityDecision(this.systemInfo, 'lifecycle_test');
  },
};

export const enhancedMethods = {
  capabilityStateColor,
};
