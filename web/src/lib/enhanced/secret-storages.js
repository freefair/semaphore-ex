import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
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
  keyName(accessKeyId) {
    return this.localKeys.find((key) => key.id === accessKeyId)?.name || `#${accessKeyId}`;
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
  async openSyncHistory(storage) {
    if (!storage) {
      return;
    }
    this.syncHistoryStorage = storage;
    this.syncHistoryDialog = true;
    this.syncHistoryLoading = true;
    this.syncHistoryError = '';
    try {
      this.syncHistory = (
        await axios.get(
          `/api/project/${this.projectId}/secret_storages/${storage.id}/sync/history?limit=25`,
        )
      ).data;
    } catch (err) {
      this.syncHistoryError = getErrorMessage(err);
    } finally {
      this.syncHistoryLoading = false;
    }
  },
  syncStatusColor(status) {
    return (
      {
        succeeded: 'success',
        conflict: 'warning',
        failed: 'error',
        running: 'info',
        pending: 'info',
      }[status] || 'grey'
    );
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
