const enhancedWatch = {
  'item.type': function itemType(type) {
    if (type !== 'ssh') {
      this.generateSSHKey = false;
    }
  },
  generateSSHKey(enabled) {
    if (enabled) {
      this.clearGeneratedSSHKeyInput(false);
    }
  },
  'item.source_storage_id': {
    handler(storageId) {
      if (this.item?.source_storage_type !== 'vault' || storageId == null) {
        return;
      }
      const storage = this.runtimeSecretStorages.find((candidate) => candidate.id === storageId);
      if (storage && !this.item.source_storage_mount) {
        this.$set(this.item, 'source_storage_mount', storage.params?.mount || 'secret');
      }
    },
  },
};

export default enhancedWatch;
