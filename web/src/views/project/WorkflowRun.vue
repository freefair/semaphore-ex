<template>
  <div>
    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title>
        <router-link :to="`/project/${projectId}/workflows`">
          {{ $t('workflows') }}
        </router-link>
        <span class="ml-2">
          / {{ workflow ? workflow.name : $t('workflowRun') }} #{{ runId }}
          <span v-if="details && details.run.version" class="text--secondary">
            · {{ details.run.version }}
          </span>
          <span v-if="details && elapsedTime" class="text--secondary">
            · {{ elapsedTime }}
          </span>
        </span>
      </v-toolbar-title>

      <v-spacer></v-spacer>

      <v-chip
        v-if="details"
        :color="statusColor(details.run.status)"
        small
        class="mr-3"
      >{{ runStatusLabel(details.run.status) }}</v-chip>
      <v-chip
        v-if="details && details.run.reconciliation_state === 'recovering'"
        small
        color="warning"
        class="mr-3"
      >{{ $t('workflowReconciliationRecovering') }}</v-chip>

      <v-chip
        v-if="parallelProgress"
        small
        outlined
        class="mr-3"
        data-testid="workflow-parallel-progress"
      >{{ $t('workflowParallelProgress', parallelProgress) }}</v-chip>

      <v-btn
        v-if="canStopRun"
        color="error"
        small
        outlined
        class="mr-3"
        :loading="stopping"
        @click="stopRun()"
      >
        <v-icon left small>mdi-stop</v-icon>
        {{ $t('stop') }}
      </v-btn>
      <v-btn
        v-if="reconciliationQuarantined && canAdminister"
        color="warning"
        small
        outlined
        class="mr-3"
        :loading="retryingReconciliation"
        @click="retryReconciliation()"
      >
        <v-icon left small>mdi-reload-alert</v-icon>
        {{ $t('workflowRetryReconciliation') }}
      </v-btn>

      <v-btn icon :title="$t('workflowToolbarZoomOut')" @click="zoomOut()">
        <v-icon>mdi-magnify-minus-outline</v-icon>
      </v-btn>
      <v-btn icon :title="$t('workflowToolbarZoomIn')" @click="zoomIn()">
        <v-icon>mdi-magnify-plus-outline</v-icon>
      </v-btn>
      <v-btn icon :title="$t('workflowToolbarFit')" @click="zoomReset()">
        <v-icon>mdi-fit-to-page-outline</v-icon>
      </v-btn>
      <v-btn icon @click="loadData()" :title="$t('refresh')">
        <v-icon>mdi-refresh</v-icon>
      </v-btn>
    </v-toolbar>

    <v-divider />

    <div class="WorkflowRun__body">
      <template v-if="details != null">
        <v-alert
          v-if="details.run.reason"
          type="error"
          dense
          text
          tile
          class="ma-0"
        >{{ details.run.reason }}</v-alert>
        <v-alert
          v-if="reconciliationQuarantined"
          type="warning"
          dense
          text
          tile
          class="ma-0"
        >
          {{ $t('workflowReconciliationQuarantined', {
            attempts: details.run.reconciliation_attempts,
            error: details.run.reconciliation_last_error,
          }) }}
        </v-alert>
        <v-alert
          v-if="reconciliationOwnership && reconciliationOwnership.recovered"
          type="info"
          dense
          text
          tile
          class="ma-0"
          data-testid="workflow-ha-ownership-transfer"
        >
          {{ workflowOwnershipSummary(reconciliationOwnership) }}
        </v-alert>

        <div class="WorkflowRun__graph">
          <WorkflowGraph
            v-if="workflow"
            ref="graph"
            :nodes="workflow.nodes || []"
            :edges="workflow.edges || []"
            :templates="templates"
            :node-statuses="nodeStatuses"
            :node-delays="nodeDelays"
            :editable="false"
            @node-selected="onNodeClicked"
          />

          <div
            v-if="pendingApprovals.length || resolvedApprovals.length"
            class="WorkflowRun__approvals"
          >
            <v-card
              v-for="a in pendingApprovals"
              :key="`approval-${a.nodeId}`"
              class="WorkflowRun__approval px-3 py-2"
              outlined
            >
              <div class="WorkflowRun__approvalText">
                <strong>#{{ a.nodeId }}</strong>
                <span class="ml-2">{{ a.prompt || $t('workflowApprovalPending') }}</span>
                <span v-if="a.deadline" class="text-caption ml-2">
                  {{ $t('workflowApprovalDeadline', { value: formatDate(a.deadline) }) }}
                </span>
                <span class="text-caption ml-2">
                  {{ $t('workflowApprovalProgress', {
                    count: a.contribution_count || 0,
                    minimum: a.minimum_distinct_approvers || 1,
                    mode: a.mode || 'any_of',
                  }) }}
                </span>
                <span v-if="!a.eligible" class="text-caption text--secondary ml-2">
                  {{ $t('workflowApprovalNotEligible') }}
                </span>
              </div>
              <v-text-field
                v-if="a.eligible"
                v-model="approvalComments[a.nodeId]"
                :label="$t('workflowApprovalComment')"
                maxlength="1024"
                dense
                hide-details
                class="ml-3 WorkflowRun__approvalComment"
              />
              <v-spacer />
              <v-btn
                v-if="a.eligible"
                small
                color="success"
                class="ml-3"
                @click="resolveApproval(a.nodeId, 'approved')"
              >{{ $t('workflowApprove') }}</v-btn>
              <v-btn
                v-if="a.eligible"
                small
                color="error"
                class="ml-2"
                @click="resolveApproval(a.nodeId, 'rejected')"
              >{{ $t('workflowReject') }}</v-btn>
            </v-card>
            <v-card
              v-for="a in resolvedApprovals"
              :key="`approval-${a.nodeId}`"
              class="WorkflowRun__approval px-3 py-2"
              outlined
            >
              <div class="WorkflowRun__approvalText">
                <strong>#{{ a.nodeId }}</strong>
                <span class="ml-2">{{ a.prompt || $t('workflowApprovalPending') }}</span>
                <span class="ml-2 text-caption">{{ approvalStatusLabel(a.status) }}</span>
                <span v-if="a.resolved_by_user_id" class="ml-2 text-caption">
                  {{ $t('workflowApprovalDecidedBy', { id: a.resolved_by_user_id }) }}
                </span>
              </div>
            </v-card>
          </div>
        </div>

        <WorkflowParameterAudit :details="details" />

        <WorkflowRunArtifacts
          :workflow="workflow"
          :artifacts="artifacts"
          :file-artifacts="fileArtifacts"
          :resolved-artifact-inputs="resolvedArtifactInputs"
          :file-artifact-download-errors="fileArtifactDownloadErrors"
          :downloading-artifact-id="downloadingArtifactId"
          @copy-checksum="copyArtifactChecksum"
          @download="downloadFileArtifact"
        />
      </template>

      <div v-else class="pa-4 text-center">
        <v-progress-circular indeterminate color="primary" />
      </div>
    </div>
  </div>
</template>

<style lang="scss">
.WorkflowRun {

  &__body {
    height: calc(100vh - 65px);
    flex: 1 1 auto;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }

  &__graph {
    position: relative;
    flex: 1 1 auto;
    min-height: 0;
  }

  &__approvals {
    position: absolute;
    left: 50%;
    bottom: 16px;
    transform: translateX(-50%);
    display: flex;
    flex-direction: column;
    gap: 8px;
    max-width: 92%;
    z-index: 5;
  }

  &__approval {
    display: flex;
    align-items: center;
    border-left: 3px solid #ff9800 !important;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25) !important;
  }

  &__approvalText {
    font-size: 13px;
    word-break: break-word;
  }

  &__approvalComment {
    min-width: 180px;
  }
}

@media (max-width: 600px) {
  .WorkflowRun {
    &__approvals {
      left: 12px;
      right: 12px;
      transform: none;
      max-width: none;
    }

    &__approval {
      align-items: stretch;
      flex-wrap: wrap;
    }

    &__approvalComment {
      min-width: 100%;
      margin-left: 0 !important;
      margin-top: 8px;
    }
  }
}
</style>

<script>
import createEnhancedState from '@/lib/enhanced/workflow-run-state';

import WorkflowRunArtifacts from '@/components/enhanced/workflow/RunArtifacts.vue';
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/workflow-run';

import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import WorkflowGraph from '@/components/WorkflowGraph.vue';
import WorkflowParameterAudit from '@/components/WorkflowParameterAudit.vue';
import socket from '@/socket';

export default {
  components: { WorkflowGraph, WorkflowParameterAudit, WorkflowRunArtifacts },
  props: {
    projectId: Number,
  },
  data() {
    return {
      details: null,
      workflow: null,
      templates: [],
      ...createEnhancedState(),
      pollHandle: null,
      socketListenerId: null,
      stopping: false,
      retryingReconciliation: false,
      approvalComments: {},
    };
  },
  computed: {
    ...enhancedComputed,
    workflowId() {
      return parseInt(this.$route.params.workflowId, 10);
    },
    runId() {
      return parseInt(this.$route.params.runId, 10);
    },
    // A run can be stopped while it is still in progress (executing tasks or
    // blocked on an approval) and the user may run project tasks.
    canStopRun() {
      if (!this.details) return false;
      return this.isActiveRunStatus(this.details.run.status)
        && Boolean(this.details.effective_access?.stop);
    },
    // node.id -> raw run status, used by the graph for color + active animation.
    nodeStatuses() {
      const map = {};
      const approvals = new Map(
        (this.details?.approvals || []).map((approval) => [approval.workflow_node_id, approval]),
      );
      (this.details?.nodes || []).forEach((n) => {
        const status = n.status || (n.task && n.task.status)
          || approvals.get(n.node.id)?.status || (n.delay && n.delay.status);
        if (status) map[n.node.id] = this.normalizeNodeStatus(status);
      });
      return map;
    },
    // node.id -> resume_at, for delay nodes currently waiting — lets the graph
    // render a live countdown between polls instead of a static duration.
    nodeDelays() {
      const map = {};
      (this.details?.nodes || []).forEach((n) => {
        if (n.delay && n.delay.status === 'waiting') map[n.node.id] = n.delay.resume_at;
      });
      return map;
    },
    pendingApprovals() {
      return (this.details?.approvals || [])
        .filter((approval) => approval.status === 'pending')
        .map((approval) => ({ ...approval, nodeId: approval.workflow_node_id }));
    },
  },
  async created() {
    this.socketListenerId = socket.addListener((data) => this.onWebsocketDataReceived(data));
    await this.loadData();
    this.pollHandle = setInterval(() => {
      const status = this.details && this.details.run.status;
      if (this.isActiveRunStatus(status)) {
        this.loadData();
      } else if (this.pollHandle) {
        clearInterval(this.pollHandle);
        this.pollHandle = null;
      }
    }, 5000);
  },
  beforeDestroy() {
    if (this.pollHandle) clearInterval(this.pollHandle);
    socket.removeListener(this.socketListenerId);
  },
  methods: {
    ...enhancedMethods,
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },
    // Clicking a node that has a task (running or finished) opens its task log.
    onNodeClicked(nodeId) {
      if (nodeId == null) return;
      const entry = (this.details?.nodes || []).find((n) => n.node.id === nodeId);
      if (entry && entry.task) {
        EventBus.$emit('i-show-task', { taskId: entry.task.id });
      }
    },
    statusColor(status) {
      switch (this.normalizeNodeStatus(status)) {
        case 'success':
        case 'approved':
          return 'success';
        case 'failed':
        case 'error':
        case 'stopped':
        case 'rejected':
          return 'error';
        case 'blocked':
          return 'warning';
        case 'canceled':
        case 'skipped':
          return 'grey';
        case 'running':
        case 'pending':
          return 'primary';
        case 'approval':
        case 'stopping':
          return 'warning';
        default:
          return 'grey';
      }
    },
    zoomIn() {
      if (this.$refs.graph) this.$refs.graph.zoomIn();
    },
    zoomOut() {
      if (this.$refs.graph) this.$refs.graph.zoomOut();
    },
    zoomReset() {
      if (this.$refs.graph) this.$refs.graph.zoomReset();
    },
    async stopRun() {
      this.stopping = true;
      try {
        await axios.post(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}/stop`,
        );
        await this.loadData();
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      } finally {
        this.stopping = false;
      }
    },
    async resolveApproval(nodeId, status) {
      try {
        const response = await axios.post(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}/approvals/${nodeId}`,
          { status, comment: this.approvalComments[nodeId] || '', source: 'user' },
        );
        if ((response.data && response.data.status !== 'pending')
          || this.details?.effective_access?.view === false) {
          this.$router.push(`/project/${this.projectId}/workflows`);
          return;
        }
        await this.loadData();
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      }
    },
    async loadData() {
      try {
        const base = `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}`;
        const [details, artifacts, fileArtifacts] = await Promise.all([
          axios.get(base),
          axios.get(`${base}/artifacts`),
          axios.get(`${base}/file-artifacts`),
        ]);
        this.details = details.data;
        this.workflow = details.data.workflow;
        this.templates = details.data.templates || [];
        this.artifacts = artifacts.data || [];
        this.fileArtifacts = fileArtifacts.data || [];
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      }
    },
  },
};
</script>
