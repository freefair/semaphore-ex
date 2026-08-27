<template>
  <v-dialog v-model="dialog" max-width="900" scrollable>
    <v-card data-testid="runner-health-dialog">
      <v-card-title class="headline RunnerHealthDialog__title">
        <div class="RunnerHealthDialog__titleText">
          <div>{{ $t('runnerHealthAndHistory') }}</div>
          <div v-if="runner" class="text--secondary subtitle-1">{{ runner.name }}</div>
        </div>
        <v-btn icon :aria-label="$t('close')" @click="dialog = false">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-card-text v-if="loading" class="text-center py-8">
        <v-progress-circular indeterminate color="primary" />
      </v-card-text>

      <v-card-text v-else-if="error" class="py-4">
        <v-alert type="error" dense outlined>{{ error }}</v-alert>
      </v-card-text>

      <v-card-text v-else-if="health" class="py-2 mb-4">
        <v-subheader class="px-0">{{ $t('runnerHealth') }}</v-subheader>
        <v-card
          data-testid="runner-health-panel"
          style="background: var(--highlighted-card-bg-color)"
        >
          <v-card-text class="px-0 py-2">
            <v-simple-table
              v-if="!$vuetify.breakpoint.xsOnly"
              dense
              class="RunnerHealthDialog__healthTable"
            >
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 220px">{{ $t('status') }}</td>
                  <td>
                    <div class="RunnerHealthDialog__heartbeat">
                      <v-chip
                        data-testid="runner-heartbeat-state"
                        :color="heartbeatColor"
                        :dark="health.heartbeat_state !== 'offline'"
                        small
                      >
                        {{ heartbeatLabel }}
                      </v-chip>
                      <span class="ml-2">{{ heartbeatMessage }}</span>
                    </div>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('version') }}</td>
                  <td>{{ health.version || $t('notReported') }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('runnerPlatform') }}</td>
                  <td>{{ health.platform || $t('notReported') }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('runnerStartedAt') }}</td>
                  <td>
                    <span v-if="health.started_at">{{ health.started_at | formatDate }}</span>
                    <span v-else>{{ $t('notReported') }}</span>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('runnerUptime') }}</td>
                  <td data-testid="runner-uptime">{{ formatUptime(health.uptime_seconds) }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('lastActivity') }}</td>
                  <td>
                    <span v-if="health.last_heartbeat">
                      {{ health.last_heartbeat | formatDate }}
                    </span>
                    <span v-else>{{ $t('runnerHeartbeatNever') }}</span>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('runnerCurrentLoad') }}</td>
                  <td data-testid="runner-current-load">
                    {{ health.current_load }} / {{ health.max_parallel_tasks || '∞' }}
                  </td>
                </tr>
              </tbody>
            </v-simple-table>
            <v-list v-else class="RunnerHealthDialog__healthList">
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('status') }}</v-list-item-subtitle>
                  <div class="RunnerHealthDialog__heartbeat mt-1">
                    <v-chip
                      data-testid="runner-heartbeat-state"
                      :color="heartbeatColor"
                      :dark="health.heartbeat_state !== 'offline'"
                      small
                    >
                      {{ heartbeatLabel }}
                    </v-chip>
                    <span>{{ heartbeatMessage }}</span>
                  </div>
                </v-list-item-content>
              </v-list-item>
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('version') }}</v-list-item-subtitle>
                  <div>{{ health.version || $t('notReported') }}</div>
                </v-list-item-content>
              </v-list-item>
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('runnerPlatform') }}</v-list-item-subtitle>
                  <div>{{ health.platform || $t('notReported') }}</div>
                </v-list-item-content>
              </v-list-item>
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('runnerStartedAt') }}</v-list-item-subtitle>
                  <div v-if="health.started_at">{{ health.started_at | formatDate }}</div>
                  <div v-else>{{ $t('notReported') }}</div>
                </v-list-item-content>
              </v-list-item>
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('runnerUptime') }}</v-list-item-subtitle>
                  <div data-testid="runner-uptime">
                    {{ formatUptime(health.uptime_seconds) }}
                  </div>
                </v-list-item-content>
              </v-list-item>
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('lastActivity') }}</v-list-item-subtitle>
                  <div v-if="health.last_heartbeat">{{ health.last_heartbeat | formatDate }}</div>
                  <div v-else>{{ $t('runnerHeartbeatNever') }}</div>
                </v-list-item-content>
              </v-list-item>
              <v-list-item>
                <v-list-item-content>
                  <v-list-item-subtitle>{{ $t('runnerCurrentLoad') }}</v-list-item-subtitle>
                  <div data-testid="runner-current-load">
                    {{ health.current_load }} / {{ health.max_parallel_tasks || '∞' }}
                  </div>
                </v-list-item-content>
              </v-list-item>
            </v-list>
          </v-card-text>
        </v-card>

        <v-subheader class="px-0 mt-3">{{ $t('runnerAssignmentHistory') }}</v-subheader>
        <v-card
          data-testid="runner-history-panel"
          style="background: var(--highlighted-card-bg-color)"
        >
          <v-simple-table
            v-if="history.length && !$vuetify.breakpoint.xsOnly"
            dense
            style="background: transparent"
          >
            <thead>
              <tr>
                <th>{{ $t('runnerTask') }}</th>
                <th>{{ $t('template') }}</th>
                <th>{{ $t('status') }}</th>
                <th>{{ $t('runnerExecution') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="item in history" :key="item.task_id">
                <td>#{{ item.task_id }}</td>
                <td>{{ item.template_name }}</td>
                <td>{{ item.status }}</td>
                <td style="min-width: 190px">
                  <div>
                    <span class="text--secondary">{{ $t('started') }}:</span>
                    <span v-if="item.start"> {{ item.start | formatDate }}</span>
                    <span v-else> —</span>
                  </div>
                  <div>
                    <span class="text--secondary">{{ $t('end') }}:</span>
                    <span v-if="item.end"> {{ item.end | formatDate }}</span>
                    <span v-else> —</span>
                  </div>
                </td>
              </tr>
            </tbody>
          </v-simple-table>
          <v-list v-else-if="history.length" class="RunnerHealthDialog__historyList">
            <v-list-item v-for="item in history" :key="item.task_id">
              <v-list-item-content>
                <v-list-item-title class="font-weight-medium" style="white-space: normal">
                  #{{ item.task_id }} — {{ item.template_name }}
                </v-list-item-title>
                <v-list-item-subtitle class="mt-1">
                  {{ item.status }}
                </v-list-item-subtitle>
                <div class="text-body-2 mt-2">
                  <div>
                    <span class="text--secondary">{{ $t('started') }}:</span>
                    <span v-if="item.start"> {{ item.start | formatDate }}</span>
                    <span v-else> —</span>
                  </div>
                  <div>
                    <span class="text--secondary">{{ $t('end') }}:</span>
                    <span v-if="item.end"> {{ item.end | formatDate }}</span>
                    <span v-else> —</span>
                  </div>
                </div>
              </v-list-item-content>
            </v-list-item>
          </v-list>
          <v-card-text v-else class="text--secondary">
            {{ $t('runnerNoCompletedAssignments') }}
          </v-card-text>
          <v-card-actions v-if="hasMore">
            <v-spacer />
            <v-btn
              data-testid="runner-history-load-more"
              text
              color="primary"
              :loading="historyLoading"
              @click="loadMore"
            >
              {{ $t('loadOlderAssignments') }}
            </v-btn>
          </v-card-actions>
        </v-card>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    value: Boolean,
    projectId: Number,
    runner: Object,
  },

  data() {
    return {
      health: null,
      history: [],
      hasMore: false,
      nextBefore: null,
      loading: false,
      historyLoading: false,
      error: null,
    };
  },

  computed: {
    dialog: {
      get() {
        return this.value;
      },
      set(value) {
        this.$emit('input', value);
      },
    },

    heartbeatColor() {
      return {
        online: 'success',
        offline: 'error',
        webhook: 'info',
      }[this.health?.heartbeat_state] || 'blue-grey';
    },

    heartbeatLabel() {
      return this.$t({
        online: 'online',
        offline: 'offline',
        webhook: 'runnerWebhook',
      }[this.health?.heartbeat_state] || 'runnerUnknown');
    },

    heartbeatMessage() {
      if (this.health?.heartbeat_state === 'webhook') {
        return this.$t('runnerWebhookHeartbeatHint');
      }
      if (this.health?.heartbeat_age_seconds == null) {
        return this.$t('runnerHeartbeatNever');
      }
      if (this.health.heartbeat_state === 'offline') {
        return this.$t('runnerHeartbeatStale', {
          age: this.health.heartbeat_age_seconds,
          boundary: this.health.heartbeat_timeout_seconds,
        });
      }
      return this.$t('runnerHeartbeatHealthy', {
        age: this.health.heartbeat_age_seconds,
        boundary: this.health.heartbeat_timeout_seconds,
      });
    },
  },

  watch: {
    value(open) {
      if (open) {
        this.load();
      }
    },
  },

  methods: {
    async load() {
      if (!this.projectId || !this.runner) {
        return;
      }
      this.loading = true;
      this.error = null;
      try {
        const base = `/api/project/${this.projectId}/runners/${this.runner.id}`;
        const [healthResponse, historyResponse] = await Promise.all([
          axios.get(`${base}/health`),
          axios.get(`${base}/history`, { params: { count: 10 } }),
        ]);
        this.health = healthResponse.data;
        this.history = historyResponse.data.items || [];
        this.hasMore = !!historyResponse.data.has_more;
        this.nextBefore = historyResponse.data.next_before || null;
      } catch (error) {
        this.error = getErrorMessage(error);
      } finally {
        this.loading = false;
      }
    },

    async loadMore() {
      if (!this.nextBefore || this.historyLoading) {
        return;
      }
      this.historyLoading = true;
      try {
        const { data } = await axios.get(
          `/api/project/${this.projectId}/runners/${this.runner.id}/history`,
          { params: { count: 10, before: this.nextBefore } },
        );
        this.history = [...this.history, ...(data.items || [])];
        this.hasMore = !!data.has_more;
        this.nextBefore = data.next_before || null;
      } catch (error) {
        this.error = getErrorMessage(error);
      } finally {
        this.historyLoading = false;
      }
    },

    formatUptime(seconds) {
      if (seconds == null) {
        return this.$t('notReported');
      }
      const days = Math.floor(seconds / 86400);
      const hours = Math.floor((seconds % 86400) / 3600);
      const minutes = Math.floor((seconds % 3600) / 60);
      const parts = [];
      if (days) parts.push(`${days}d`);
      if (hours || days) parts.push(`${hours}h`);
      parts.push(`${minutes}m`);
      return parts.join(' ');
    },
  },
};
</script>

<style scoped>
.RunnerHealthDialog__title {
  display: flex;
  flex-wrap: nowrap;
}

.RunnerHealthDialog__titleText {
  flex: 1 1 auto;
  min-width: 0;
  white-space: normal;
}

.RunnerHealthDialog__title .v-btn {
  flex: 0 0 auto;
  align-self: flex-start;
}

.RunnerHealthDialog__healthTable {
  background: transparent !important;
}

.RunnerHealthDialog__healthTable td {
  height: auto;
  padding-top: 8px;
  padding-bottom: 8px;
  white-space: normal;
  word-break: break-word;
}

.RunnerHealthDialog__heartbeat {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 0;
}

.RunnerHealthDialog__historyList .v-list-item + .v-list-item {
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.RunnerHealthDialog__healthList .v-list-item + .v-list-item {
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

@media (max-width: 600px) {
  .RunnerHealthDialog__title {
    padding: 16px;
  }

  .RunnerHealthDialog__titleText {
    font-size: 1.25rem;
    line-height: 1.35;
  }

  .RunnerHealthDialog__healthTable td:first-child {
    width: 105px !important;
    min-width: 105px;
  }
}
</style>
