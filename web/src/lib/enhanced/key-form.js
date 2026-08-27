import { findCapabilityDecision } from '@/lib/capabilities';

const enhancedComputed = {
  runtimeDecision() {
    return findCapabilityDecision(this.systemInfo, 'runtime_secrets');
  },
  runtimeCanExecute() {
    return this.runtimeDecision?.access?.includes('execute') || false;
  },
  runtimeCanWrite() {
    return this.runtimeDecision?.access?.includes('write') || false;
  },
  runtimeReferenceDisabled() {
    return this.formSaving || !this.canEditSecrets || this.isSynced || !this.runtimeCanWrite;
  },
  runtimeTypeDisabled() {
    return this.sourceStorageType === 'vault' && !this.runtimeCanWrite;
  },
  runtimeSecretStorages() {
    return (this.secretStorages || []).filter((storage) => (
      storage.type === 'vault' || storage.type === 'openbao'
    ));
  },
};

export default enhancedComputed;
