import ItemFormBase from '@/components/ItemFormBase';
import { findCapabilityDecision } from '@/lib/capabilities';
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
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
  canGenerateSSHKey() {
    return this.isNew && !this.isReadOnly && !this.sourceStorageType && this.item.type === 'ssh';
  },
  generatedSSHKeyMetadata() {
    const metadata = this.item?.generated_ssh_key;
    if (!metadata || !['ed25519', 'rsa-3072'].includes(metadata.algorithm)
        || typeof metadata.public_key !== 'string' || typeof metadata.fingerprint !== 'string') {
      return null;
    }
    const expectedPrefix = metadata.algorithm === 'ed25519' ? 'ssh-ed25519 ' : 'ssh-rsa ';
    if (!metadata.public_key.startsWith(expectedPrefix) || !metadata.fingerprint.startsWith('SHA256:')) {
      return null;
    }
    return metadata;
  },
};

export const enhancedMethods = {
  afterReset() {
    this.generateSSHKey = false;
    this.generatedSSHKeyAlgorithm = 'ed25519';
  },
  async save(data = {}) {
    if (!this.generateSSHKey) {
      return ItemFormBase.methods.save.call(this, data);
    }
    return this.saveGeneratedSSHKey();
  },
  async saveGeneratedSSHKey() {
    this.formError = null;
    if (!this.$refs.form.validate()) {
      this.$emit('error', {});
      return null;
    }
    this.formSaving = true;
    try {
      const response = (await axios({
        method: 'post',
        url: `/api/project/${this.projectId}/keys/generate`,
        responseType: 'json',
        data: this.generatedSSHKeyRequest(),
      })).data;
      this.clearGeneratedSSHKeyInput();
      this.$emit('save', {
        item: response.key,
        response,
        action: 'generated',
      });
      return response.key;
    } catch (err) {
      this.formError = getErrorMessage(err);
      this.$emit('error', { message: this.formError });
      return null;
    } finally {
      this.formSaving = false;
    }
  },
  clearGeneratedSSHKeyInput(resetGeneration = true) {
    if (resetGeneration) {
      this.generateSSHKey = false;
    }
    if (this.item?.ssh) {
      this.$set(this.item.ssh, 'private_key', '');
      this.$set(this.item.ssh, 'passphrase', '');
    }
  },
  generatedSSHKeyRequest() {
    return {
      name: this.item.name,
      login: this.item.ssh.login || '',
      algorithm: this.generatedSSHKeyAlgorithm,
    };
  },
};
