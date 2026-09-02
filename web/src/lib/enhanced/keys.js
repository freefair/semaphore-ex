import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import EventBus from '@/event-bus';

const enhancedMethods = {
  async onKeySaved(event) {
    await this.loadItems();
    this.showGeneratedKeyResult(event?.response);
  },
  showGeneratedKeyResult(response) {
    if (!response || typeof response.public_key !== 'string' || typeof response.fingerprint !== 'string'
        || !['ed25519', 'rsa-3072'].includes(response.algorithm)
        || Object.prototype.hasOwnProperty.call(response, 'private_key')
        || Object.prototype.hasOwnProperty.call(response, 'passphrase')) {
      return;
    }
    const prefix = response.algorithm === 'ed25519' ? 'ssh-ed25519 ' : 'ssh-rsa ';
    if (!response.public_key.startsWith(prefix) || !response.fingerprint.startsWith('SHA256:')) {
      return;
    }
    this.generatedKeyResult = {
      public_key: response.public_key,
      fingerprint: response.fingerprint,
      algorithm: response.algorithm,
    };
    this.generatedKeyDialog = true;
  },
  canRotateSSHKey(item) {
    return this.can(this.USER_PERMISSIONS.manageProjectResources)
        && item.type === 'ssh' && !item.synchronized && !item.source_storage_type;
  },
  async beginSSHKeyRotation(item) {
    if (!this.canRotateSSHKey(item) || this.rotationLoading) {
      return;
    }
    this.rotationLoading = true;
    this.rotationError = null;
    try {
      this.rotationRefs = (await axios({
        method: 'get',
        url: `/api/project/${this.projectId}/keys/${item.id}/refs`,
        responseType: 'json',
      })).data;
      this.rotationItem = item;
      this.rotationAlgorithm = item.generated_ssh_key?.algorithm || 'ed25519';
      this.rotationDialog = true;
    } catch (err) {
      EventBus.$emit('i-snackbar', {
        color: 'error',
        text: getErrorMessage(err),
      });
    } finally {
      this.rotationLoading = false;
    }
  },
  async rotateSSHKey() {
    if (!this.rotationItem || this.rotationLoading) {
      return;
    }
    this.rotationLoading = true;
    this.rotationError = null;
    try {
      const response = (await axios({
        method: 'post',
        url: `/api/project/${this.projectId}/keys/${this.rotationItem.id}/rotate`,
        responseType: 'json',
        data: this.rotationRequest(),
      })).data;
      this.rotationDialog = false;
      await this.loadItems();
      this.showGeneratedKeyResult(response);
    } catch (err) {
      this.rotationError = getErrorMessage(err);
    } finally {
      this.rotationLoading = false;
    }
  },
  rotationRequest() {
    return {
      algorithm: this.rotationAlgorithm,
      confirm_rotation: true,
    };
  },
};

export default enhancedMethods;
