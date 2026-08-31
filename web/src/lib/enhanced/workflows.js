import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import { USER_PERMISSIONS } from '@/lib/constants';

const enhancedMethods = {
  allowActions() {
    return this.can(USER_PERMISSIONS.startWorkflows);
  },
  hasRunInputs(workflow) {
    return (workflow.parameters || []).length > 0
        || (workflow.nodes || []).some((node) => {
          const policy = node.override_policy || {};
          return (policy.inventory_ids || []).length
            || (policy.environment_ids || []).length
            || policy.allow_arguments
            || policy.allow_branch;
        });
  },
  startSelectedWorkflow(payload) {
    return this.runWorkflow(this.selectedWorkflow, payload);
  },
  async openApprovalInbox() {
    this.approvalInboxDialog = true;
    this.approvalInboxLoading = true;
    try {
      this.approvalInbox = (await axios.get(
        `/api/project/${this.projectId}/workflow-approvals`,
      )).data || [];
    } catch (err) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
    } finally {
      this.approvalInboxLoading = false;
    }
  },
  openApprovalRun(approval) {
    this.approvalInboxDialog = false;
    this.$router.push(
      `/project/${this.projectId}/workflows/${approval.workflow_template_id}/runs/${approval.workflow_run_id}`,
    );
  },
};

export default enhancedMethods;
