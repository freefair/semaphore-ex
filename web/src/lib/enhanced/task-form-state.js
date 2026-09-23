export default function createEnhancedState() {
  return {
    executionPreflight: null,
    executionPreflightPayloadSignature: null,
    executionPreflightInitialized: false,
    executionPreflightLoading: false,
    executionPreflightError: null,
    executionPreflightUnavailable: false,
    executionPreflightRequestId: 0,
    executionPreflightTimer: null,
    deploymentWindowBlock: null,
    deploymentWindowOverrideCategory: null,
    deploymentWindowOverrideReference: '',
    deploymentWindowOverrideConfirmed: false,
  };
}
