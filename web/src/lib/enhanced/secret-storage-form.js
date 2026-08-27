import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
  isRuntimeProvider() {
    return this.item?.type === 'vault' || this.item?.type === 'openbao';
  },
  isManagedOutbound() {
    return this.isRuntimeProvider && this.item?.sync_direction === 'outbound';
  },
  managedLocalKeys() {
    return (this.localKeys || []).filter(
      (key) => !key.owner
          && !key.source_storage_type
          && ['string', 'login_password', 'ssh'].includes(key.type),
    );
  },
  runtimeProviderNotice() {
    if (this.isManagedOutbound) {
      return 'Managed provider: Semaphore can resolve fields at runtime and synchronize selected local keys outbound.';
    }
    return 'Runtime provider: Semaphore reads one named field only during task execution.';
  },
  runtimeCredentialLabel() {
    switch (this.item?.params?.auth_method) {
      case 'approle':
        return 'AppRole secret ID';
      case 'kubernetes':
        return 'Kubernetes JWT';
      default:
        return 'Token';
    }
  },
  connectionHealthMessage() {
    if (!this.connectionHealth) {
      return '';
    }
    if (this.connectionHealth.state === 'healthy') {
      return `Connection healthy (${this.connectionHealth.latency_ms || 0} ms)`;
    }
    return `Connection failed: ${this.connectionHealth.error_category || 'unavailable'}`;
  },
};

export const enhancedMethods = {
  async testConnection() {
    this.connectionTesting = true;
    this.connectionHealth = null;
    try {
      this.connectionHealth = (
        await axios.post(`/api/project/${this.projectId}/secret_storages/${this.itemId}/test`)
      ).data;
    } catch (err) {
      this.connectionHealth = err.response?.data || {
        state: 'failed',
        error_category: getErrorMessage(err),
      };
    } finally {
      this.connectionTesting = false;
    }
  },
};
