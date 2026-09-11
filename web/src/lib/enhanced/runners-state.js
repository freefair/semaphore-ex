export default function createEnhancedState() {
  return {
    deletingRunnerIds: [],
    cacheCleaningRunnerIds: [],
    runnerHealthDialog: false,
    selectedHealthRunner: null,
  };
}
