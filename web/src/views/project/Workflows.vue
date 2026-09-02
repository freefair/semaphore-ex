<template xmlns:v-slot="http://www.w3.org/1999/XSL/Transform">
  <div v-if="items == null">
    <v-progress-linear
      indeterminate
      color="primary darken-2"
    ></v-progress-linear>
  </div>
  <div v-else>
    <YesNoDialog
      :title="$t('deleteWorkflow')"
      :text="$t('askDeleteWorkflow')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />
    <WorkflowRunDialog
      v-if="selectedWorkflow"
      ref="workflowRunDialog"
      v-model="runDialog"
      :workflow="selectedWorkflow"
      :project-id="projectId"
      :loading="starting"
      @start="startSelectedWorkflow"
    />
    <v-dialog v-model="approvalInboxDialog" max-width="720">
      <v-card data-testid="workflow-approval-inbox">
        <v-card-title>{{ $t('workflowApprovalInbox') }}</v-card-title>
        <v-progress-linear v-if="approvalInboxLoading" indeterminate color="primary" />
        <v-list v-else-if="approvalInbox.length > 0" two-line>
          <v-list-item
            v-for="approval in approvalInbox"
            :key="approval.id"
            @click="openApprovalRun(approval)"
          >
            <v-list-item-content>
              <v-list-item-title>
                {{ approval.workflow_name || $t('workflowRun') }} #{{ approval.workflow_run_id }}
              </v-list-item-title>
              <v-list-item-subtitle>
                {{ approval.prompt }}
                <span class="ml-2">
                  {{ approval.contribution_count || 0 }}/
                  {{ approval.minimum_distinct_approvers || 1 }}
                  · {{ approval.mode || 'any_of' }}
                </span>
              </v-list-item-subtitle>
            </v-list-item-content>
            <v-list-item-action v-if="approval.deadline">
              <span class="text-caption">{{ approval.deadline | formatDate }}</span>
            </v-list-item-action>
          </v-list-item>
        </v-list>
        <v-card-text v-else>{{ $t('workflowApprovalInboxEmpty') }}</v-card-text>
      </v-card>
    </v-dialog>

    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title>
        {{ $t('workflows') }}
      </v-toolbar-title>
      <v-spacer></v-spacer>

      <v-btn
        text
        class="mr-1"
        @click="openApprovalInbox()"
      >
        <v-icon left small>mdi-account-check</v-icon>
        {{ $t('workflowApprovalInbox') }}
      </v-btn>

      <v-btn
        color="primary"
        class="mr-1"
        @click="openEditor('new')"
        v-if="can(USER_PERMISSIONS.editWorkflows)"
      >
        {{ $t('newWorkflow') }}
      </v-btn>

      <v-btn icon @click="settingsSheet = true">
        <v-icon>mdi-cog</v-icon>
      </v-btn>
    </v-toolbar>

    <v-divider style="margin-top: -1px;"/>

    <v-data-table
      hide-default-footer
      class="mt-4 workflows-table"
      single-expand
      show-expand
      :headers="filteredHeaders"
      :items="items"
      :items-per-page="Number.MAX_VALUE"
      :expanded.sync="openedItems"
    >
      <template v-slot:item.name="{ item }">
        <router-link :to="`/project/${projectId}/workflows/${item.id}`">
          {{ item.name }}
        </router-link>
      </template>

      <template v-slot:item.version="{ item }">
        <span v-if="item.last_run && item.last_run.version">{{ item.last_run.version }}</span>
        <div v-else>&mdash;</div>
      </template>

      <template v-slot:item.status="{ item }">
        <div class="mt-2 mb-2 d-flex" v-if="item.last_run != null">
          <v-chip :color="statusColor(item.last_run.status)" small>
            {{ item.last_run.status }}
          </v-chip>
        </div>
        <div v-else class="mt-3 mb-2 d-flex" style="color: gray;">{{ $t('notLaunched') }}</div>
      </template>

      <template v-slot:item.last_run="{ item }">
        <div class="mt-2 mb-2" v-if="item.last_run != null" style="line-height: 1">
          <router-link
            :to="`/project/${projectId}/workflows/${item.id}/runs/${item.last_run.id}`"
          >#{{ item.last_run.id }}</router-link>
          <div style="color: gray; font-size: 14px;">
            {{ item.last_run.start | formatDate }}
          </div>
        </div>
        <div v-else>&mdash;</div>
      </template>

      <template v-slot:item.actions="{ item }">
        <v-btn-toggle dense :value-comparator="() => false">
          <v-btn
            v-if="item.effective_access && item.effective_access.start"
            @click="runWorkflow(item)"
            :title="$t('workflowRunNow')"
          >
            <v-icon>mdi-play</v-icon>
          </v-btn>
        </v-btn-toggle>
      </template>

      <template v-slot:expanded-item="{ headers, item }">
        <td
          :colspan="headers.length"
          v-if="openedItems.some((w) => w.id === item.id)"
        >
          <v-data-table
            style="border: 1px solid lightgray; border-radius: 6px; margin: 10px 0;"
            hide-default-footer
            :headers="runHeaders"
            :items="runs[item.id] || []"
            :loading="runsLoading[item.id]"
            :items-per-page="5"
            :no-data-text="$t('notLaunched')"
          >
            <template v-slot:item.id="{ item: run }">
              <router-link :to="`/project/${projectId}/workflows/${item.id}/runs/${run.id}`">
                #{{ run.id }}
              </router-link>
            </template>
            <template v-slot:item.status="{ item: run }">
              <v-chip :color="statusColor(run.status)" small>{{ run.status }}</v-chip>
            </template>
            <template v-slot:item.version="{ item: run }">
              {{ run.version || '—' }}
            </template>
            <template v-slot:item.start="{ item: run }">
              {{ run.start | formatDate }}
            </template>
            <template v-slot:item.end="{ item: run }">
              {{ run.end | formatDate }}
            </template>
          </v-data-table>
        </td>
      </template>
    </v-data-table>

    <TableSettingsSheet
      v-model="settingsSheet"
      table-name="project__workflow"
      :headers="headers"
      @change="onTableSettingsChange"
    />
  </div>
</template>
<style lang="scss">
@import '~vuetify/src/styles/settings/variables';

.workflows-table .text-start:first-child {
  padding-right: 0 !important;
}

@media #{map-get($display-breakpoints, 'sm-and-down')} {
  .workflows-table .v-data-table__mobile-row:first-child {
    display: none !important;
  }
}
</style>
<script>
import enhancedMethods from '@/lib/enhanced/workflows';

import ItemListPageBase from '@/components/ItemListPageBase';
import TableSettingsSheet from '@/components/TableSettingsSheet.vue';
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';

const PREFLIGHT_FINGERPRINT_HEADER = 'X-Semaphore-Preflight-Fingerprint';
const PREFLIGHT_REVIEW_HEADER = ['X-Semaphore-Preflight', 'Token'].join('-');

export default {
  components: {
    TableSettingsSheet,
    WorkflowRunDialog,
  },
  mixins: [ItemListPageBase],

  data() {
    return {
      settingsSheet: null,
      filteredHeaders: [],
      openedItems: [],
      runs: {},
      runsLoading: {},
      selectedWorkflow: null,
      runDialog: false,
      starting: false,
      approvalInboxDialog: false,
      approvalInboxLoading: false,
      approvalInbox: [],
    };
  },

  computed: {
    runHeaders() {
      return [
        { text: this.$i18n.t('workflowRun'), value: 'id', sortable: false },
        { text: this.$i18n.t('status'), value: 'status', sortable: false },
        { text: this.$i18n.t('version'), value: 'version', sortable: false },
        { text: this.$i18n.t('start'), value: 'start', sortable: false },
        { text: this.$i18n.t('end'), value: 'end', sortable: false },
      ];
    },
  },

  watch: {
    openedItems(items) {
      items.forEach((item) => this.loadRuns(item.id));
    },
  },

  methods: {
    ...enhancedMethods,
    // Workflow actions are independently policy-gated per row. Requiring the
    // generic resource-management bit here hides a permitted start action for
    // narrow workflow roles before effective_access can decide visibility.

    statusColor(status) {
      switch (status) {
        case 'success':
        case 'approved':
          return 'success';
        case 'failed':
        case 'error':
        case 'stopped':
        case 'rejected':
          return 'error';
        case 'running':
        case 'pending':
          return 'primary';
        case 'approval':
          return 'warning';
        default:
          return 'grey';
      }
    },

    openEditor(id) {
      this.$router.push(
        id === 'new'
          ? `/project/${this.projectId}/workflows/new`
          : `/project/${this.projectId}/workflows/${id}/edit`,
      );
    },

    async loadRuns(workflowId) {
      if (this.runs[workflowId] != null) {
        return;
      }
      this.$set(this.runsLoading, workflowId, true);
      try {
        const runs = (await axios({
          method: 'get',
          url: `/api/project/${this.projectId}/workflows/${workflowId}/runs`,
          responseType: 'json',
        })).data;
        this.$set(this.runs, workflowId, runs);
      } catch (err) {
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      } finally {
        this.$set(this.runsLoading, workflowId, false);
      }
    },

    onTableSettingsChange({ headers }) {
      this.filteredHeaders = headers;
    },

    getHeaders() {
      return [
        {
          text: this.$i18n.t('name'),
          value: 'name',
        },
        {
          value: 'actions',
          sortable: false,
          width: '0%',
        },
        {
          text: this.$i18n.t('version'),
          value: 'version',
          sortable: false,
        },
        {
          text: this.$i18n.t('status'),
          value: 'status',
          sortable: false,
        },
        {
          text: this.$i18n.t('workflowLastRun'),
          value: 'last_run',
          sortable: false,
        },
      ];
    },

    getItemsUrl() {
      return `/api/project/${this.projectId}/workflows`;
    },

    getSingleItemUrl() {
      return `/api/project/${this.projectId}/workflows/${this.itemId}`;
    },

    getEventName() {
      return 'i-workflow';
    },

    async runWorkflow(workflow, request) {
      if (request === undefined) {
        this.selectedWorkflow = workflow;
        this.runDialog = true;
        return;
      }
      const payload = request?.payload === undefined ? request : request.payload;
      const review = request?.payload === undefined ? null : request.review;
      this.starting = true;
      try {
        const run = (await axios({
          method: 'post',
          url: `/api/project/${this.projectId}/workflows/${workflow.id}/run`,
          data: payload || {},
          headers: review ? {
            [PREFLIGHT_FINGERPRINT_HEADER]: review.fingerprint,
            [PREFLIGHT_REVIEW_HEADER]: review.reviewToken,
          } : {},
          responseType: 'json',
        })).data;
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('workflowRunStarted'),
        });
        this.runDialog = false;
        this.$router.push(
          `/project/${this.projectId}/workflows/${workflow.id}/runs/${run.id}`,
        );
      } catch (err) {
        const fresh = err?.response?.data?.preflight;
        if (err?.response?.status === 409 && fresh && this.$refs.workflowRunDialog) {
          this.$refs.workflowRunDialog.adoptExecutionPreflight(fresh, payload);
          return;
        }
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      } finally {
        this.starting = false;
      }
    },
  },
};
</script>
