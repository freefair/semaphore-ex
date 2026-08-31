import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
  canAdminister() {
    return Boolean(this.details?.effective_access?.administer);
  },
  reconciliationQuarantined() {
    return this.details?.run?.reconciliation_state === 'quarantined';
  },
  reconciliationOwnership() {
    return this.details?.run?.reconciliation_ownership || null;
  },
  resolvedApprovals() {
    return (this.details?.approvals || [])
      .filter((approval) => approval.status !== 'pending')
      .map((approval) => ({ ...approval, nodeId: approval.workflow_node_id }));
  },
  elapsedTime() {
    if (!this.details) return '';
    const { run } = this.details;
    return this.formatElapsed(run.start || run.created, run.end);
  },
  parallelProgress() {
    const max = this.workflow?.max_parallel_tasks;
    if (!max || !this.details) return null;
    const active = (this.details.nodes || []).filter(
      (node) => ['queued', 'running'].includes(node.status),
    ).length;
    return { active, max };
  },
  resolvedArtifactInputs() {
    if (!this.details) return [];
    return (this.details.nodes || []).flatMap((entry) => (
      (entry.artifact_inputs || []).map((input) => ({
        ...input,
        consumer_node_id: entry.node.id,
      }))
    ));
  },
  artifactMetadataCount() {
    return this.artifacts.length + this.resolvedArtifactInputs.length;
  },
};

export const enhancedMethods = {
  workflowOwnershipSummary(ownership) {
    return this.$t('workflowReconciliationOwnershipTransferred', {
      owner: (ownership.owner_boot_id || '').slice(0, 8),
      transfers: ownership.transfer_count || 0,
      lag: ownership.reconciliation_lag_seconds || 0,
    });
  },
  runStatusLabel(status) {
    const labels = {
      stopping: this.$t('workflowRunStopping'),
      canceled: this.$t('workflowRunStopped'),
    };
    return labels[status] || status;
  },
  artifactAvailabilityColor(availability) {
    if (availability === 'available') return 'success';
    if (availability === 'invalid') return 'error';
    return 'grey';
  },
  schemaSummary(schema) {
    return JSON.stringify(schema || {});
  },
  nodeLabel(nodeId) {
    const node = (this.workflow?.nodes || []).find((entry) => entry.id === nodeId);
    return node?.display_name ? `#${nodeId} ${node.display_name}` : `#${nodeId}`;
  },
  normalizeNodeStatus(status) {
    switch (status) {
      case 'succeeded': return 'success';
      case 'queued': return 'waiting';
      default: return status;
    }
  },
  isActiveRunStatus(status) {
    return ['pending', 'queued', 'running', 'approval', 'stopping'].includes(status);
  },
  formatElapsed(start, end) {
    if (!start) return '';
    const finishedAt = new Date(end || Date.now()).getTime();
    const duration = Math.max(0, finishedAt - new Date(start).getTime());
    const totalSeconds = Math.floor(duration / 1000);
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
  },
  onWebsocketDataReceived(data) {
    if (data.type !== 'update' || data.project_id !== this.projectId) return;
    const belongsToRun = (this.details?.nodes || []).some(
      (node) => node.task && node.task.id === data.task_id,
    );
    if (belongsToRun) this.loadData();
  },
  async retryReconciliation() {
    this.retryingReconciliation = true;
    try {
      await axios.post(
        `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}/retry-reconcile`,
      );
      await this.loadData();
    } catch (err) {
      EventBus.$emit('i-snackbar', {
        color: 'error',
        text: getErrorMessage(err),
      });
    } finally {
      this.retryingReconciliation = false;
    }
  },
  approvalStatusLabel(status) {
    const labels = {
      approved: this.$t('workflowApprovalApproved'),
      rejected: this.$t('workflowApprovalRejected'),
      expired: this.$t('workflowApprovalExpired'),
      canceled: this.$t('workflowApprovalCanceled'),
    };
    return labels[status] || status;
  },
  formatDate(value) {
    return value ? new Date(value).toLocaleString() : '';
  },
};
