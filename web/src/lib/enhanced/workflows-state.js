export default function createEnhancedState() {
  return {
    selectedWorkflow: null,
    runDialog: false,
    starting: false,
    approvalInboxDialog: false,
    approvalInboxLoading: false,
    approvalInbox: [],
  };
}
