<template>
  <section class="TaskSummary px-5 py-5" aria-labelledby="task-summary-title">
    <div class="d-flex align-center flex-wrap mb-4 TaskSummary__heading">
      <div>
        <h2 id="task-summary-title" class="text-h6 mb-1">Task summary</h2>
        <p class="text-body-2 text--secondary mb-0">
          Persisted Ansible results for this task.
        </p>
      </div>
      <v-spacer />
      <v-btn
        v-if="featureAvailable && !loading"
        text
        small
        color="primary"
        data-testid="task-summary-refresh"
        @click="loadData"
      >
        <v-icon left small>mdi-refresh</v-icon>
        Refresh
      </v-btn>
    </div>

    <v-alert
      v-if="summaryDisplayState === 'unavailable'"
      text
      type="info"
      class="PageAlert"
      data-testid="task-summary-unavailable"
    >
      Task summaries are not enabled.
    </v-alert>

    <div
      v-else-if="summaryDisplayState === 'loading'"
      data-testid="task-summary-loading"
      aria-live="polite"
    >
      <v-progress-linear indeterminate color="primary" class="mb-3" />
      <span class="text-body-2 text--secondary">Loading persisted task results…</span>
    </div>

    <v-alert
      v-else-if="summaryDisplayState === 'error'"
      type="error"
      outlined
      data-testid="task-summary-error"
    >
      <div class="d-flex align-center flex-wrap">
        <span>Task results could not be loaded. The underlying task result is unchanged.</span>
        <v-spacer />
        <v-btn small text color="error" @click="loadData">Retry</v-btn>
      </div>
    </v-alert>

    <v-alert
      v-else-if="summaryDisplayState === 'unsupported'"
      type="warning"
      outlined
      data-testid="task-summary-unsupported"
    >
      Runner result version {{ summary.runner_result_version }} is not supported by this server.
      The task log remains available.
    </v-alert>

    <v-alert
      v-else-if="summaryDisplayState === 'empty'"
      type="info"
      outlined
      data-testid="task-summary-empty"
    >
      This task produced no supported host results.
    </v-alert>

    <div v-else-if="summary" data-testid="task-summary-content">
      <v-alert
        v-if="pageError"
        type="error"
        outlined
        dismissible
        data-testid="task-summary-page-error"
        @input="pageError = null"
      >
        This result page could not be loaded. The currently displayed results were kept.
      </v-alert>

      <v-alert
        v-if="summary.state === 'partial'"
        type="warning"
        outlined
        data-testid="task-summary-partial"
      >
        <strong>Partial task summary.</strong>
        {{ summary.diagnostic || 'Some runner results are unavailable.' }}
        Available results are shown below; the underlying task status is unchanged.
      </v-alert>

      <v-alert
        v-else-if="summary.state === 'collecting'"
        type="info"
        outlined
        data-testid="task-summary-collecting"
      >
        Results are still being collected. Refresh after the task finishes to see the final summary.
      </v-alert>

      <div class="TaskSummary__metrics mb-6">
        <article
          class="TaskSummaryMetric TaskSummaryMetric--ok"
          data-testid="task-summary-ok-hosts"
        >
          <span class="TaskSummaryMetric__value">{{ summary.ok_hosts }}</span>
          <span class="TaskSummaryMetric__label">Healthy hosts</span>
        </article>
        <article
          class="TaskSummaryMetric TaskSummaryMetric--failed"
          data-testid="task-summary-failed-hosts"
        >
          <span class="TaskSummaryMetric__value">{{ summary.failed_hosts }}</span>
          <span class="TaskSummaryMetric__label">Failed hosts</span>
        </article>
        <article
          class="TaskSummaryMetric TaskSummaryMetric--total"
          data-testid="task-summary-total-hosts"
        >
          <span class="TaskSummaryMetric__value">{{ summary.total_hosts }}</span>
          <span class="TaskSummaryMetric__label">Total hosts</span>
        </article>
      </div>

      <v-btn-toggle v-model="tab" mandatory dense class="mb-4 TaskSummary__tabs">
        <v-btn value="errors" data-testid="task-summary-errors-tab">
          Failures
          <v-chip x-small class="ml-2" :color="summary.failed_hosts ? 'error' : undefined">
            {{ summary.failed_hosts }}
          </v-chip>
        </v-btn>
        <v-btn value="hosts" data-testid="task-summary-hosts-tab">Hosts</v-btn>
        <v-btn value="stages" data-testid="task-summary-stages-tab">Stages</v-btn>
      </v-btn-toggle>

      <div v-if="tab === 'errors'" data-testid="task-summary-errors">
        <v-alert v-if="errorsPage.length === 0" text type="success">
          No failed or unreachable hosts in this result page.
        </v-alert>
        <v-data-table
          v-else
          :headers="errorHeaders"
          :items="errorsPage"
          :items-per-page="pageSize"
          hide-default-footer
          single-expand
          show-expand
          item-key="event_id"
        >
          <template v-slot:item.error="{ item }">
            <span class="TaskSummary__error-preview">{{ item.error }}</span>
          </template>
          <template v-slot:item.output_time="{ item }">
            <a :href="rawLogURL" target="_blank" rel="noopener">
              {{ formatTime(item.output_time) }}
            </a>
          </template>
          <template v-slot:expanded-item="{ headers, item }">
            <td :colspan="headers.length" class="pa-3">
              <pre class="TaskSummary__error-detail">{{ item.error }}</pre>
            </td>
          </template>
        </v-data-table>
        <SummaryPagination
          :has-previous="pagination.errors.history.length > 0"
          :has-next="pagination.errors.next !== null"
          :loading="pageLoading === 'errors'"
          @previous="previousPage('errors')"
          @next="nextPage('errors')"
        />
      </div>

      <div v-else-if="tab === 'hosts'" data-testid="task-summary-hosts">
        <v-alert v-if="hostsPage.length === 0" text type="info">
          No host results on this page.
        </v-alert>
        <v-data-table
          v-else
          :headers="hostHeaders"
          :items="hostsPage"
          :items-per-page="pageSize"
          hide-default-footer
          item-key="id"
        >
          <template v-slot:item.status="{ item }">
            <v-chip x-small :color="item.status === 'failed' ? 'error' : 'success'" dark>
              {{ item.status }}
            </v-chip>
          </template>
        </v-data-table>
        <SummaryPagination
          :has-previous="pagination.hosts.history.length > 0"
          :has-next="pagination.hosts.next !== null"
          :loading="pageLoading === 'hosts'"
          @previous="previousPage('hosts')"
          @next="nextPage('hosts')"
        />
      </div>

      <div v-else data-testid="task-summary-stages">
        <v-alert v-if="stagesPage.length === 0" text type="info">
          No stage results on this page.
        </v-alert>
        <v-data-table
          v-else
          :headers="stageHeaders"
          :items="stagesPage"
          :items-per-page="pageSize"
          hide-default-footer
          item-key="id"
        />
        <SummaryPagination
          :has-previous="pagination.stages.history.length > 0"
          :has-next="pagination.stages.next !== null"
          :loading="pageLoading === 'stages'"
          @previous="previousPage('stages')"
          @next="nextPage('stages')"
        />
      </div>
    </div>
  </section>
</template>

<script>
import ProjectMixin from '@/components/ProjectMixin';

const SummaryPagination = {
  props: {
    hasPrevious: Boolean,
    hasNext: Boolean,
    loading: Boolean,
  },
  template: `
    <div class="d-flex justify-end align-center mt-3" data-testid="task-summary-pagination">
      <v-btn small text :disabled="!hasPrevious || loading" @click="$emit('previous')">
        <v-icon left small>mdi-chevron-left</v-icon>Previous
      </v-btn>
      <v-btn small text :disabled="!hasNext || loading" @click="$emit('next')">
        Next<v-icon right small>mdi-chevron-right</v-icon>
      </v-btn>
    </div>
  `,
};

function newPaginationState() {
  return { before: null, next: null, history: [] };
}

export default {
  components: { SummaryPagination },

  props: {
    projectId: Number,
    taskId: Number,
    features: Object,
  },

  mixins: [ProjectMixin],

  data() {
    return {
      loading: false,
      pageLoading: null,
      loadError: null,
      pageError: null,
      summary: null,
      hostsPage: [],
      stagesPage: [],
      errorsPage: [],
      tab: 'errors',
      pageSize: 50,
      pagination: {
        hosts: newPaginationState(),
        stages: newPaginationState(),
        errors: newPaginationState(),
      },
      errorHeaders: [
        { text: 'Host', value: 'host', sortable: false },
        { text: 'Stage', value: 'stage', sortable: false },
        { text: 'Error', value: 'error', sortable: false },
        { text: 'Raw log', value: 'output_time', sortable: false },
      ],
      hostHeaders: [
        { text: 'Host', value: 'host', sortable: false },
        { text: 'Status', value: 'status', sortable: false },
        { text: 'Changed', value: 'changed', sortable: false },
        { text: 'Failed', value: 'failed', sortable: false },
        { text: 'Ignored', value: 'ignored', sortable: false },
        { text: 'OK', value: 'ok', sortable: false },
        { text: 'Rescued', value: 'rescued', sortable: false },
        { text: 'Skipped', value: 'skipped', sortable: false },
        { text: 'Unreachable', value: 'unreachable', sortable: false },
      ],
      stageHeaders: [
        { text: 'Stage', value: 'stage', sortable: false },
        { text: 'OK', value: 'ok', sortable: false },
        { text: 'Changed', value: 'changed', sortable: false },
        { text: 'Failed', value: 'failed', sortable: false },
        { text: 'Ignored', value: 'ignored', sortable: false },
        { text: 'Rescued', value: 'rescued', sortable: false },
        { text: 'Skipped', value: 'skipped', sortable: false },
        { text: 'Unreachable', value: 'unreachable', sortable: false },
        { text: 'Duration (ms)', value: 'duration_ms', sortable: false },
      ],
    };
  },

  computed: {
    summaryDisplayState() {
      if (!this.featureAvailable) return 'unavailable';
      if (this.loading) return 'loading';
      if (this.loadError) return 'error';
      return this.summary?.state || 'idle';
    },

    featureAvailable() {
      return Boolean(this.features?.task_summary);
    },

    rawLogURL() {
      return `/api/project/${this.projectId}/tasks/${this.taskId}/raw_output`;
    },
  },

  watch: {
    taskId() {
      this.loadData();
    },
  },

  created() {
    this.loadData();
  },

  methods: {
    resetPagination() {
      this.pagination = {
        hosts: newPaginationState(),
        stages: newPaginationState(),
        errors: newPaginationState(),
      };
    },

    pageEndpoint(kind) {
      return `/tasks/${this.taskId}/ansible/summary/${kind}`;
    },

    pageItemsProperty(kind) {
      return `${kind}Page`;
    },

    async fetchPage(kind, before = null) {
      const params = { count: this.pageSize };
      if (before !== null) {
        params.before = before;
      }
      return this.loadProjectEndpoint(this.pageEndpoint(kind), { params });
    },

    applyPage(kind, page, before) {
      this[this.pageItemsProperty(kind)] = page.items || [];
      this.pagination[kind].before = before;
      this.pagination[kind].next = page.next_cursor ?? null;
    },

    async loadData() {
      if (!this.featureAvailable) {
        this.summary = null;
        return;
      }
      this.loading = true;
      this.loadError = null;
      this.pageError = null;
      this.resetPagination();
      try {
        const [summary, hosts, stages, errors] = await Promise.all([
          this.loadProjectEndpoint(`/tasks/${this.taskId}/ansible/summary`),
          this.fetchPage('hosts'),
          this.fetchPage('stages'),
          this.fetchPage('errors'),
        ]);
        this.summary = summary;
        this.applyPage('hosts', hosts, null);
        this.applyPage('stages', stages, null);
        this.applyPage('errors', errors, null);
      } catch (error) {
        this.loadError = error;
        this.summary = null;
      } finally {
        this.loading = false;
      }
    },

    async nextPage(kind) {
      const state = this.pagination[kind];
      if (state.next === null || this.pageLoading) return;
      this.pageLoading = kind;
      this.pageError = null;
      try {
        const page = await this.fetchPage(kind, state.next);
        state.history.push(state.before);
        this.applyPage(kind, page, state.next);
      } catch (error) {
        this.pageError = error;
      } finally {
        this.pageLoading = null;
      }
    },

    async previousPage(kind) {
      const state = this.pagination[kind];
      if (state.history.length === 0 || this.pageLoading) return;
      const before = state.history[state.history.length - 1];
      this.pageLoading = kind;
      this.pageError = null;
      try {
        const page = await this.fetchPage(kind, before);
        state.history.pop();
        this.applyPage(kind, page, before);
      } catch (error) {
        this.pageError = error;
      } finally {
        this.pageLoading = null;
      }
    },

    formatTime(value) {
      if (!value) return 'Open log';
      return new Date(value).toLocaleTimeString();
    },
  },
};
</script>

<style lang="scss">
.TaskSummary {
  max-width: 1200px;
}

.TaskSummary__heading {
  gap: 12px;
}

.TaskSummary__metrics {
  display: grid;
  grid-template-columns: repeat(3, minmax(150px, 1fr));
  gap: 12px;
}

.TaskSummaryMetric {
  min-height: 112px;
  padding: 18px;
  border: 1px solid rgba(127, 127, 127, 0.3);
  border-left-width: 5px;
  border-radius: 8px;
  background: rgba(127, 127, 127, 0.06);
}

.TaskSummaryMetric--ok { border-left-color: #43a047; }
.TaskSummaryMetric--failed { border-left-color: #e53935; }
.TaskSummaryMetric--total { border-left-color: #546e7a; }

.TaskSummaryMetric__value,
.TaskSummaryMetric__label {
  display: block;
}

.TaskSummaryMetric__value {
  font-size: 2.25rem;
  font-weight: 700;
  line-height: 1.1;
}

.TaskSummaryMetric__label {
  margin-top: 8px;
  font-size: 0.8rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.TaskSummary__tabs {
  max-width: 100%;
  overflow-x: auto;
}

.TaskSummary__error-preview {
  display: inline-block;
  max-width: 420px;
  overflow: hidden;
  color: #e53935;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.TaskSummary__error-detail {
  max-height: 280px;
  margin: 0;
  padding: 12px;
  overflow: auto;
  border-radius: 6px;
  background: #263238;
  color: #fff;
  font-size: 0.82rem;
  white-space: pre-wrap;
}

@media (max-width: 700px) {
  .TaskSummary {
    padding-right: 12px !important;
    padding-left: 12px !important;
  }

  .TaskSummary__metrics {
    grid-template-columns: 1fr;
  }

  .TaskSummaryMetric {
    min-height: 88px;
  }
}
</style>
