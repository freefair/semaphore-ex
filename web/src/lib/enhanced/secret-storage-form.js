import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
  isRuntimeProvider() {
    return this.item?.type === 'vault' || this.item?.type === 'openbao';
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
      this.connectionHealth = (await axios.post(
        `/api/project/${this.projectId}/secret_storages/${this.itemId}/test`,
      )).data;
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
