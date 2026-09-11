export default function createEnhancedState() {
  return {
    executionPreflight: null,
    executionPreflightPayloadSignature: null,
    deploymentWindowBlock: null,
    deploymentWindowOverrideCategory: null,
    deploymentWindowOverrideReference: '',
    deploymentWindowOverrideConfirmed: false,
  };
}
