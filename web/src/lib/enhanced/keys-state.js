export function createEnhancedState() {
  return {
    generatedKeyDialog: false,
    generatedKeyResult: null,
    rotationDialog: false,
    rotationItem: null,
    rotationRefs: null,
    rotationAlgorithm: 'ed25519',
    rotationLoading: false,
    rotationError: null,
    generatedSSHKeyAlgorithms: [
      { id: 'ed25519', name: 'Ed25519 (recommended)' },
      { id: 'rsa-3072', name: 'RSA 3072 (compatibility)' },
    ],
  };
}

export const enhancedWatch = {
  generatedKeyDialog(value) {
    if (!value) {
      this.generatedKeyResult = null;
    }
  },
  rotationDialog(value) {
    if (!value) {
      this.rotationItem = null;
      this.rotationRefs = null;
      this.rotationError = null;
    }
  },
};
