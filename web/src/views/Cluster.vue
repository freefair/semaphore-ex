<template>
  <div
    class="ClusterPage"
    data-testid="cluster-page"
    :data-state="status == null ? 'loading' : 'ready'"
  >
    <v-dialog v-model="clearDialog" max-width="450" persistent>
      <v-card
        class="ClusterClearDialog"
        data-testid="clusterClearDialog"
        :data-state="clearing ? 'loading' : 'ready'"
      >
        <v-card-title
          data-testid="clusterClearDialog-title"
        >{{ $t('clearTasksTitle') }}</v-card-title>
        <v-card-text>
          <v-alert
            type="warning"
            density="compact"
            variant="tonal"
            data-testid="clusterClearDialog-warning"
          >
            {{ $t('clearTasksWarning') }}
          </v-alert>
          <div class="mb-2">{{ $t('clearTasksGroups') }}</div>
          <v-checkbox
            v-model="clearScope.queue"
            :label="$t('queue')"
            hide-details
            density="compact"
            data-testid="clusterClearDialog-queue"
          />
          <v-checkbox
            v-model="clearScope.running"
            :label="$t('running')"
            hide-details
            density="compact"
            data-testid="clusterClearDialog-running"
          />
          <v-checkbox
            v-model="clearScope.active"
            :label="$t('activeByProject')"
            hide-details
            density="compact"
            data-testid="clusterClearDialog-active"
          />
          <v-checkbox
            v-model="clearScope.aliases"
            :label="$t('aliases')"
            hide-details
            density="compact"
            data-testid="clusterClearDialog-aliases"
          />
          <v-checkbox
            v-model="clearScope.claims"
            :label="$t('claims')"
            hide-details
            density="compact"
            data-testid="clusterClearDialog-claims"
          />
          <v-checkbox
            v-model="clearScope.runtime_fields"
            :label="$t('runtimeFields')"
            hide-details
            density="compact"
            data-testid="clusterClearDialog-runtimeFields"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn
            variant="text"
            @click="clearDialog = false"
            data-testid="clusterClearDialog-cancel"
          >{{ $t('cancel') }}</v-btn>
          <v-btn
            color="error"
            variant="elevated"
            :disabled="!isAnyGroupSelected || clearing"
            :loading="clearing"
            @click="clearTasks()"
            data-testid="clusterClearDialog-clear"
          >
            {{ $t('clearTasksFromRedis') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-toolbar flat>
      <v-btn
        icon
        variant="text"
        class="mr-4"
        @click="returnToProjects()"
        data-testid="cluster-back"
      >
        <v-icon>mdi-arrow-left</v-icon>
      </v-btn>
      <v-toolbar-title data-testid="cluster-title">{{ $t('clusterDashboard') }}</v-toolbar-title>
      <v-spacer />
      <v-btn
        color="error"
        variant="elevated"
        @click="openClearDialog()"
        :disabled="!canClearClusterTasks"
        data-testid="cluster-clear"
      >
        <v-icon start>mdi-delete-sweep</v-icon>
        {{ $t('clearTasksFromRedis') }}
      </v-btn>
    </v-toolbar>

    <v-divider />

    <v-alert
      v-if="!features.high_availability"
      variant="tonal"
      color="amber darken-3"
      class="PageAlert"
    >
      High availability is not enabled.
    </v-alert>

    <v-alert
      v-if="status && !status.ha_enabled"
      type="info"
      density="compact"
      variant="tonal"
      data-testid="cluster-haDisabled"
    >
      {{ $t('haDisabledBanner') }}
    </v-alert>

    <v-defaults-provider :defaults="tableDefaults">
      <div class="pa-4">
      <!-- Nodes -->
      <v-card v-if="status && (status.nodes || status.coordinator)" class="mb-4" variant="outlined">
        <v-card-title class="subtitle-1">
          {{ $t('nodes') }}
          <v-spacer />
          <v-chip v-if="status.health" size="x-small" class="mr-1" color="success">
            {{ $t('clusterNodeReady') }}: {{ status.health.ready }}
          </v-chip>
          <v-chip
            v-if="status.health && status.health.stale"
            size="x-small"
            class="mr-1"
            color="warning"
          >
            {{ $t('clusterNodeStale') }}: {{ status.health.stale }}
          </v-chip>
          <v-chip
            v-if="status.health && status.health.incompatible"
            size="x-small"
            class="mr-1"
            color="error"
          >
            {{ $t('clusterNodeIncompatible') }}: {{ status.health.incompatible }}
          </v-chip>
          <v-chip v-if="status.health && status.health.draining" size="x-small" color="grey">
            {{ $t('clusterNodeDraining') }}: {{ status.health.draining }}
          </v-chip>
          <v-chip
            v-if="status.coordinator"
            size="x-small"
            class="ml-1"
            :color="coordinatorStateColor(status.coordinator)"
            dark
          >
            {{ $t('clusterLiveEvents') }}: {{ coordinatorState(status.coordinator) }}
          </v-chip>
          <v-chip
            v-if="status.coordinator && status.coordinator.workflow_progression"
            size="x-small"
            variant="outlined"
            class="ml-1"
            data-testid="cluster-workflow-progression"
          >
            {{ workflowProgressionLabel(status.coordinator.workflow_progression) }}
          </v-chip>
        </v-card-title>
        <v-data-table
          :headers="nodeHeaders"
          :items="status.nodes || []"
          :items-per-page="20"
          density="compact"
        >
          <template v-slot:item.node_id="{ item }">
            <code>{{ item.node_id }}</code>
            <v-chip v-if="item.is_self" size="x-small" class="ml-2">{{ $t('thisNode') }}</v-chip>
          </template>
          <template v-slot:item.alive="{ item }">
            <v-chip size="x-small" :color="nodeStateColor(item)" dark>
              {{ nodeState(item) }}
            </v-chip>
          </template>
          <template v-slot:item.last_heartbeat="{ item }">
            {{ formatTime(item.last_heartbeat) }}
          </template>
          <template v-slot:item.started_at="{ item }">
            {{ formatTime(item.started_at) }}
          </template>
          <template v-slot:item.version="{ item }">
            {{ nodeVersion(item) }}
          </template>
        </v-data-table>
      </v-card>

        <!-- Redis -->
        <v-card v-if="status && status.redis" class="mb-4" flat border data-testid="cluster-redis">
          <v-card-title class="ClusterPage__cardTitle">{{ $t('redisStatus') }}</v-card-title>
          <v-card-text>
            <v-row density="compact">
              <v-col cols="12" md="6">
                <v-row density="compact">
                  <v-col>
                    <div class="text-body-small text-grey">Addr</div>
                    <code data-testid="cluster-redisAddr">{{ status.redis.addr }}</code>
                  </v-col>
                  <v-col>
                    <div class="text-body-small text-grey">{{ $t('connected') }}</div>
                    <v-chip
                      size="x-small"
                      :color="status.redis.connected ? 'success' : 'error'"
                      variant="flat"
                      data-testid="cluster-redisConnected"
                    >
                      {{ status.redis.connected ? 'YES' : 'NO' }}
                    </v-chip>
                  </v-col>
                  <v-col data-testid="cluster-redisVersion">
                    <div class="text-body-small text-grey">{{ $t('version') }}</div>
                    {{ status.redis.version || '—' }}
                  </v-col>
                  <v-col data-testid="cluster-redisUsedMemory">
                    <div class="text-body-small text-grey">{{ $t('usedMemory') }}</div>
                    {{ status.redis.used_memory || '—' }}
                  </v-col>
                  <v-col data-testid="cluster-redisTotalKeys">
                    <div class="text-body-small text-grey">{{ $t('totalKeys') }}</div>
                    {{ status.redis.total_keys }}
                  </v-col>
                </v-row>
                <div class="text-body-small text-grey mt-3">{{ $t('keyGroups') }}</div>
                <v-chip
                  v-for="(count, group) in status.redis.key_groups || {}"
                  :key="group"
                  size="small"
                  class="mr-2 mt-1"
                  :data-testid="`cluster-redisKeyGroup-${group}`"
                >
                  {{ group }}: {{ count }}
                </v-chip>
              </v-col>
              <v-col cols="12" md="6">
                <div class="text-body-small text-grey">{{ $t('redisMemoryChart') }}</div>
                <RedisMemoryChart
                  data-testid="cluster-redisChart"
                  :history="memoryHistory"
                  :window-ms="memoryWindowMs"
                  :now-ts="memoryNow"
                />
              </v-col>
            </v-row>
          </v-card-text>
        </v-card>

        <!-- Task records -->
        <v-card flat border data-testid="cluster-taskRecords">
          <v-card-title class="ClusterPage__cardTitle">{{ $t('taskRecords') }}</v-card-title>
          <v-tabs v-model="tab" data-testid="cluster-tabs">
            <v-tab
              :value="0"
              data-testid="cluster-tab-queue"
            >{{ $t('queue') }} ({{ queueRows.length }})</v-tab>
            <v-tab
              :value="1"
              data-testid="cluster-tab-running"
            >{{ $t('running') }} ({{ runningRows.length }})</v-tab>
            <v-tab
              :value="2"
              data-testid="cluster-tab-activeByProject"
            >{{ $t('activeByProject') }} ({{ activeRows.length }})</v-tab>
            <v-tab
              :value="3"
              data-testid="cluster-tab-aliases"
            >{{ $t('aliases') }} ({{ aliasRows.length }})</v-tab>
            <v-tab
              :value="4"
              data-testid="cluster-tab-claims"
            >{{ $t('claims') }} ({{ claimRows.length }})</v-tab>
          </v-tabs>
          <v-tabs-window v-model="tab">
            <v-tabs-window-item :value="0">
              <v-data-table
                :headers="taskHeaders"
                :items="queueRows"
                density="compact"
                :page-text="queueRows.length === 0 ? '–' : undefined"
                data-testid="cluster-queueTable"
                :data-state="tasks == null
                  ? 'loading' : (queueRows.length === 0 ? 'empty' : 'ready')"
              />
            </v-tabs-window-item>
            <v-tabs-window-item :value="1">
              <v-data-table
                :headers="taskHeaders"
                :items="runningRows"
                density="compact"
                :page-text="runningRows.length === 0 ? '–' : undefined"
                data-testid="cluster-runningTable"
                :data-state="tasks == null
                  ? 'loading' : (runningRows.length === 0 ? 'empty' : 'ready')"
              />
            </v-tabs-window-item>
            <v-tabs-window-item :value="2">
              <v-data-table
                :headers="taskHeaders"
                :items="activeRows"
                density="compact"
                :page-text="activeRows.length === 0 ? '–' : undefined"
                data-testid="cluster-activeByProjectTable"
                :data-state="tasks == null
                  ? 'loading' : (activeRows.length === 0 ? 'empty' : 'ready')"
              />
            </v-tabs-window-item>
            <v-tabs-window-item :value="3">
              <v-data-table
                :headers="aliasHeaders"
                :items="aliasRows"
                density="compact"
                :page-text="aliasRows.length === 0 ? '–' : undefined"
                data-testid="cluster-aliasesTable"
                :data-state="tasks == null
                  ? 'loading' : (aliasRows.length === 0 ? 'empty' : 'ready')"
              />
            </v-tabs-window-item>
            <v-tabs-window-item :value="4">
              <v-data-table
                :headers="claimHeaders"
                :items="claimRows"
                density="compact"
                :page-text="claimRows.length === 0 ? '–' : undefined"
                data-testid="cluster-claimsTable"
                :data-state="tasks == null
                  ? 'loading' : (claimRows.length === 0 ? 'empty' : 'ready')"
              />
            </v-tabs-window-item>
          </v-tabs-window>
        </v-card>
      </div>
    </v-defaults-provider>
  </div>
</template>

<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/cluster';

import axios from 'axios';
import EventBus from '@/event-bus';
import RedisMemoryChart from '@/components/RedisMemoryChart.vue';

const MEMORY_WINDOW_MS = 60 * 60 * 1000;

export default {
  components: { RedisMemoryChart },

  props: {
    features: Object,
    isAdmin: Boolean,
  },

  data() {
    return {
      status: null,
      tasks: null,
      tab: 0,
      clearDialog: false,
      clearing: false,
      clearScope: {
        queue: true,
        running: false,
        active: false,
        aliases: false,
        claims: false,
        runtime_fields: false,
      },
      updateTimer: null,
      memoryHistory: [],
      memoryNow: Date.now(),
      memoryWindowMs: MEMORY_WINDOW_MS,
    };
  },

  computed: {
    tableDefaults() {
      return {
        VDataTable: {
          itemsPerPageText: this.$t('rowsPerPage'),
        },
      };
    },

    ...enhancedComputed,
    isAnyGroupSelected() {
      return Object.values(this.clearScope).some((v) => v);
    },

    nodeHeaders() {
      return [
        { title: this.$t('nodeId'), key: 'node_id' },
        { title: this.$t('status'), key: 'alive' },
        { title: this.$t('lastHeartbeat'), key: 'last_heartbeat' },
        { title: this.$t('startedAt'), key: 'started_at' },
        { title: this.$t('version'), key: 'version' },
      ];
    },

    taskHeaders() {
      return [
        { title: 'Task', key: 'task_id' },
        { title: this.$t('project'), key: 'project_id' },
        { title: 'Template', key: 'template_id' },
        { title: this.$t('status'), key: 'status' },
        { title: 'Runner', key: 'runner_id' },
        { title: this.$t('username'), key: 'username' },
        { title: this.$t('owningNode'), key: 'node_id' },
      ];
    },

    aliasHeaders() {
      return [
        { title: this.$t('aliases'), key: 'alias' },
        { title: 'Task', key: 'task_id' },
      ];
    },

    claimHeaders() {
      return [{ title: 'Task', key: 'task_id' }];
    },

    queueRows() {
      return (this.tasks && this.tasks.queue) || [];
    },

    runningRows() {
      return (this.tasks && this.tasks.running) || [];
    },

    activeRows() {
      const byProj = (this.tasks && this.tasks.active_by_project) || {};
      return Object.values(byProj).reduce((acc, list) => acc.concat(list || []), []);
    },

    aliasRows() {
      const aliases = (this.tasks && this.tasks.aliases) || {};
      return Object.keys(aliases).map((alias) => ({ alias, task_id: aliases[alias] }));
    },

    claimRows() {
      return ((this.tasks && this.tasks.claims) || []).map((id) => ({ task_id: id }));
    },
  },

  created() {
    this.reload();
    this.updateTimer = setInterval(() => {
      if (!document.hidden) {
        this.reload();
      }
    }, 10000);
  },

  unmounted() {
    clearInterval(this.updateTimer);
  },

  methods: {
    ...enhancedMethods,
    async returnToProjects() {
      EventBus.$emit('i-open-last-project');
    },

    formatTime(value) {
      if (!value || value.startsWith('0001-01-01')) {
        return '—';
      }
      return new Date(value).toLocaleString();
    },

    async reload() {
      try {
        this.status = (
          await axios({
            method: 'get',
            url: '/api/cluster',
            responseType: 'json',
          })
        ).data;

        this.recordMemorySample();

        this.tasks = (
          await axios({
            method: 'get',
            url: '/api/cluster/tasks',
            responseType: 'json',
          })
        ).data;
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: `Failed to load cluster status: ${err.message}`,
        });
      }
    },

    recordMemorySample() {
      const now = Date.now();
      this.memoryNow = now;
      const redis = this.status && this.status.redis;
      if (!redis || !redis.connected) {
        return;
      }
      this.memoryHistory.push({
        time: now,
        bytes: redis.used_memory_bytes || 0,
      });
      const cutoff = now - this.memoryWindowMs;
      const firstInWindow = this.memoryHistory.findIndex((p) => p.time >= cutoff);
      if (firstInWindow > 0) {
        this.memoryHistory.splice(0, firstInWindow);
      }
    },

    openClearDialog() {
      this.clearScope = {
        queue: true,
        running: false,
        active: false,
        aliases: false,
        claims: false,
        runtime_fields: false,
      };
      this.clearDialog = true;
    },

    async clearTasks() {
      this.clearing = true;
      try {
        const res = (
          await axios({
            method: 'delete',
            url: '/api/cluster/tasks',
            responseType: 'json',
            data: { scope: this.clearScope },
          })
        ).data;

        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: `Cleared ${res.deleted_keys} record(s) from the backend.`,
        });
        this.clearDialog = false;
        await this.reload();
      } catch (err) {
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: `Failed to clear tasks: ${err.message}`,
        });
      } finally {
        this.clearing = false;
      }
    },
  },
};
</script>
<style lang="scss">
// The dense check boxes of the clear dialog (`v-checkbox dense`): a 20 px box icon 2 px under the
// top of the line of its label, as in Vuetify 2 (Vuetify 4 centres it in the taller control). The
// dialog is rendered outside the page, hence a class of its own.
.ClusterClearDialog .v-checkbox.v-input--density-compact .v-selection-control__input {
  align-items: flex-start;

  > .v-icon {
    display: block;
    margin-top: 2px;
    font-size: 20px;
    line-height: 20px;
  }
}

// The /cluster screen as Vuetify 2 drew it.
.ClusterPage {
  // Card titles: `subtitle-1` of Vuetify 2 (the codemod made it `text-body-large`, the body-1 of
  // Vuetify 2 on a 24 px line): 16 px on a 28 px line, letter-spacing .009375em.
  .ClusterPage__cardTitle {
    font-size: 1rem;
    font-weight: 400;
    line-height: 1.75rem;
    letter-spacing: .009375em;
  }

  // Chips of Vuetify 2: x-small 16 px high, small 24 px (Vuetify 4: 18 and 26).
  .v-chip.v-chip--size-x-small {
    --v-chip-height: 16px;
  }

  .v-chip.v-chip--size-small {
    --v-chip-height: 24px;
  }

  .v-data-table {
    // On a phone the rows of a dense table are cards of 48 px lines and the sort select keeps
    // its 48 px header: `dense` of Vuetify 2 reached neither `.v-data-table__mobile-row` nor the
    // mobile header, only the no-data line (32 px).
    &.v-table--density-compact:has(.v-data-table-headers--mobile) {
      --v-table-header-height: 48px;

      .v-data-table__tr--mobile {
        --v-table-row-height: 48px;
      }
    }

    // The rows-per-page value («20») ends 2 px before the arrow of its select, not 4 px as the
    // shared footer (assets/scss/vuetify2.scss) has it.
    > .v-data-table-footer > .v-data-table-footer__items-per-page > .v-select .v-field__input {
      padding-inline-end: 2px;
    }
  }
}
</style>
