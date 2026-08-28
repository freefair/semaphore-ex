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
            v-if="canResolveApprovals && pendingApprovals.length"
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
                <span class="ml-2">{{ a.message || $t('workflowApprovalPending') }}</span>
              </div>
              <v-spacer />
              <v-btn
                small
                color="success"
                class="ml-3"
                @click="resolveApproval(a.nodeId, 'approved')"
              >{{ $t('workflowApprove') }}</v-btn>
              <v-btn
                small
                color="error"
                class="ml-2"
                @click="resolveApproval(a.nodeId, 'rejected')"
              >{{ $t('workflowReject') }}</v-btn>
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
</style>

<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/workflow-run';

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
      USER_PERMISSIONS,
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
      (this.details?.nodes || []).forEach((n) => {
        const status = n.status || (n.task && n.task.status)
          || (n.approval && n.approval.status) || (n.delay && n.delay.status);
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
      return (this.details?.nodes || [])
        .filter((n) => n.approval && n.approval.status === 'pending')
        .map((n) => ({ nodeId: n.node.id, message: n.node.approval_message }));
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
        await axios.post(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/runs/${this.runId}/approvals/${nodeId}`,
          { status },
        );
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
