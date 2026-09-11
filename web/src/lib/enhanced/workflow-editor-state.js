export default function createEnhancedState() {
  return {
    validating: false,
    dirty: false,
    validationState: 'idle',
    validationIssues: [],
    conflict: null,
    versionsDialog: false,
    crossProjectGrantsDialog: false,
    crossProjectReferences: [],
    crossProjectReferencesLoading: false,
  };
}
