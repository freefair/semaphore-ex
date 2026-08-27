import { findCapabilityDecision } from '@/lib/capabilities';

export const enhancedComputed = {
  runtimeDecision() {
    return findCapabilityDecision(this.systemInfo, 'runtime_secrets');
  },
  dialogStorageType() {
    if (this.itemId === 'new') {
      return this.itemType;
    }
    return this.items?.find((item) => item.id === this.itemId)?.type || this.itemType;
  },
  runtimeCanRead() {
    return this.runtimeDecision?.access?.includes('read') || false;
  },
  runtimeCanWrite() {
    return this.runtimeDecision?.access?.includes('write') || false;
  },
  runtimeCanExecute() {
    return this.runtimeDecision?.access?.includes('execute') || false;
  },
};

export const enhancedMethods = {
  formatCapabilityValue(value) {
    return (value || 'unknown').replace(/_/g, ' ');
  },
};
