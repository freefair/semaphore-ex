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
  async beforeLoadItems() {
    this.localKeys = await this.loadProjectResources('keys');
  },
  formatCapabilityValue(value) {
    return (value || 'unknown').replace(/_/g, ' ');
  },
  createSyncRequestID() {
    if (window.crypto && typeof window.crypto.randomUUID === 'function') {
      return `manual:${window.crypto.randomUUID()}`;
    }
    return `manual:${Date.now()}:${Math.random().toString(36).slice(2, 14)}`;
  },
  openSyncHistory(storage) {
    this.$refs.syncHistory.open(storage);
  },
  formatTimestamp(value) {
    return value ? new Date(value).toLocaleString() : 'Never';
  },
  lastAttempt(item) {
    if (!item.last_synced_at) {
      return item.last_sync_failed_at;
    }
    if (!item.last_sync_failed_at) {
      return item.last_synced_at;
    }
    return new Date(item.last_synced_at) > new Date(item.last_sync_failed_at)
      ? item.last_synced_at
      : item.last_sync_failed_at;
  },
};
