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
      >{{ details.run.status }}</v-chip>

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
              </div>
              <v-text-field
                v-if="canResolveApprovals"
                v-model="approvalComments[a.nodeId]"
                :label="$t('workflowApprovalComment')"
                maxlength="1024"
                dense
                hide-details
                class="ml-3 WorkflowRun__approvalComment"
              />
              <v-spacer />
              <v-btn
                v-if="canResolveApprovals"
                small
                color="success"
                class="ml-3"
                @click="resolveApproval(a.nodeId, 'approved')"
              >{{ $t('workflowApprove') }}</v-btn>
              <v-btn
                v-if="canResolveApprovals"
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

        <v-expansion-panels
          v-if="artifactMetadataCount > 0"
          accordion
          flat
          tile
          class="WorkflowRun__artifactPanel"
          data-testid="workflow-artifact-metadata"
        >
          <v-expansion-panel>
            <v-expansion-panel-header class="py-2">
              {{ $t('workflowArtifactRunMetadata') }} ({{ artifactMetadataCount }})
            </v-expansion-panel-header>
            <v-expansion-panel-content>
              <div class="WorkflowRun__artifactContent">
                <section v-if="artifacts.length > 0">
                  <div class="text-subtitle-2 mb-1">
                    {{ $t('workflowArtifactOutputs') }}
                  </div>
                  <v-list dense class="py-0">
                    <v-list-item
                      v-for="artifact in artifacts"
                      :key="`artifact-${artifact.workflow_node_id}-${artifact.name}`"
                      class="px-0"
                    >
                      <v-list-item-content>
                        <v-list-item-title class="WorkflowRun__artifactTitle">
                          <strong>{{ artifact.name }}</strong>
                          <span class="text--secondary">
                            · {{ nodeLabel(artifact.workflow_node_id) }}
                          </span>
                          <v-chip
                            x-small
                            class="ml-2"
                            :color="artifactAvailabilityColor(artifact.availability)"
                          >{{ artifact.availability }}</v-chip>
                        </v-list-item-title>
                        <v-list-item-subtitle class="WorkflowRun__artifactDetail">
                          {{ schemaSummary(artifact.schema) }}
                          <span v-if="artifact.sensitive">
                            · {{ $t('workflowArtifactSensitiveRedacted') }}
                          </span>
                          <span v-else>· {{ $t('workflowArtifactNotSensitive') }}</span>
                          <span v-if="artifact.producer_task_id">
                            · {{ $t('workflowArtifactProducer', {
                              task: artifact.producer_task_id,
                              attempt: artifact.producer_attempt,
                            }) }}
                          </span>
                          <span v-if="artifact.diagnostic"> · {{ artifact.diagnostic }}</span>
                        </v-list-item-subtitle>
                      </v-list-item-content>
                    </v-list-item>
                  </v-list>
                </section>

                <section v-if="resolvedArtifactInputs.length > 0">
                  <div class="text-subtitle-2 mb-1">
                    {{ $t('workflowArtifactInputs') }}
                  </div>
                  <v-list dense class="py-0">
                    <v-list-item
                      v-for="input in resolvedArtifactInputs"
                      :key="`artifact-input-${input.consumer_node_id}-${input.name}`"
                      class="px-0"
                    >
                      <v-list-item-content>
                        <v-list-item-title class="WorkflowRun__artifactTitle">
                          <strong>{{ input.name }}</strong>
                          <span class="text--secondary">
                            · {{ nodeLabel(input.consumer_node_id) }} ←
                            {{ nodeLabel(input.source_node_id) }}.{{ input.output }}
                          </span>
                          <v-chip
                            x-small
                            class="ml-2"
                            :color="artifactAvailabilityColor(input.availability)"
                          >{{ input.availability }}</v-chip>
                        </v-list-item-title>
                        <v-list-item-subtitle class="WorkflowRun__artifactDetail">
                          {{ input.required
                            ? $t('workflowArtifactRequired')
                            : $t('workflowArtifactOptional') }}
                          <span v-if="input.sensitive">
                            · {{ $t('workflowArtifactSensitiveRedacted') }}
                          </span>
                          <span v-if="input.producer_task_id">
                            · {{ $t('workflowArtifactProducer', {
                              task: input.producer_task_id,
                              attempt: input.producer_attempt,
                            }) }}
                          </span>
                        </v-list-item-subtitle>
                      </v-list-item-content>
                    </v-list-item>
                  </v-list>
                </section>
              </div>
            </v-expansion-panel-content>
          </v-expansion-panel>
        </v-expansion-panels>
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

  &__artifactPanel {
    flex: 0 0 auto;
    border-top: 1px solid rgba(127, 127, 127, 0.2);
  }

  &__artifactContent {
    max-height: 260px;
    overflow-y: auto;
  }

  &__artifactTitle,
  &__artifactDetail {
    white-space: normal;
    overflow-wrap: anywhere;
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
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import PermissionsCheck from '@/components/PermissionsCheck';
import WorkflowGraph from '@/components/WorkflowGraph.vue';
import WorkflowParameterAudit from '@/components/WorkflowParameterAudit.vue';
import { USER_PERMISSIONS } from '@/lib/constants';
import socket from '@/socket';

export default {
  components: { WorkflowGraph, WorkflowParameterAudit },
  mixins: [PermissionsCheck],
  props: {
    projectId: Number,
  },
  data() {
    return {
      details: null,
      workflow: null,
      templates: [],
      artifacts: [],
      pollHandle: null,
      socketListenerId: null,
      stopping: false,
      approvalComments: {},
      USER_PERMISSIONS,
    };
  },
  computed: {
    workflowId() {
      return parseInt(this.$route.params.workflowId, 10);
    },
    runId() {
      return parseInt(this.$route.params.runId, 10);
    },
    canResolveApprovals() {
      return this.can(USER_PERMISSIONS.runProjectTasks);
    },
    // A run can be stopped while it is still in progress (executing tasks or
    // blocked on an approval) and the user may run project tasks.
    canStopRun() {
      if (!this.details) return false;
      return this.isActiveRunStatus(this.details.run.status)
        && this.can(USER_PERMISSIONS.runProjectTasks);
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
          return 'warning';
        default:
          return 'grey';
      }
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
      return ['pending', 'queued', 'running', 'approval'].includes(status);
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
        await axios.post(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}/approvals/${nodeId}`,
          { status, comment: this.approvalComments[nodeId] || '', source: 'user' },
        );
        await this.loadData();
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
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
    async loadData() {
      try {
        const base = `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}`;
        const [details, artifacts] = await Promise.all([
          axios.get(base),
          axios.get(`${base}/artifacts`),
        ]);
        this.details = details.data;
        this.workflow = details.data.workflow;
        this.templates = details.data.templates || [];
        this.artifacts = artifacts.data || [];
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
