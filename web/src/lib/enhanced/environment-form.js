const enhancedComputed = {
  managedSecretStorages() {
    return (this.secretStorages || []).filter(
      (storage) => !['vault', 'openbao'].includes(storage.type),
    );
  },
};

export default enhancedComputed;
