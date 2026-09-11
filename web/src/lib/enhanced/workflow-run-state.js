export default function createEnhancedState() {
  return {
    artifacts: [],
    fileArtifacts: [],
    fileArtifactDownloadErrors: {},
    downloadingArtifactId: null,
  };
}
