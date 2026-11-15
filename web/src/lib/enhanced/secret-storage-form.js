import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
  isRuntimeProvider() {
    return this.item?.type === 'vault' || this.item?.type === 'openbao';
  },
  isInfisicalPAM() {
    return this.item?.type === 'infisical';
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
      const identity = this.connectionHealth.identity
        ? `Connected as ${this.connectionHealth.identity}` : 'Connected';
      return `${identity} (${this.connectionHealth.latency_ms || 0} ms)`;
    }
    return this.connectionHealth.message
      || `Connection failed: ${this.connectionHealth.error_category || 'unavailable'}`;
  },
};

export const enhancedMethods = {
  async testConnection() {
    this.connectionTesting = true;
    this.connectionHealth = null;
    const started = Date.now();
    try {
      // The identity test answers who the storage acts as, or says what to
      // fix when the store refuses it.
      const identity = (
        await axios.post(`/api/project/${this.projectId}/secret_storages/${this.itemId}/identity`)
      ).data;
      this.connectionHealth = {
        state: 'healthy',
        identity: identity.name,
        latency_ms: Date.now() - started,
      };
    } catch (err) {
      this.connectionHealth = {
        state: 'failed',
        message: err.response?.data?.error || getErrorMessage(err),
        error_category: err.response?.data?.reason,
      };
    } finally {
      this.connectionTesting = false;
    }
  },
};
