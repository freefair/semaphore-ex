<template>
  <div>
    <template v-if="lifecycleDecision">
      <v-list-subheader class="px-0 mt-2">{{ $t('lifecycleCapability') }}</v-list-subheader>
      <v-card
        data-testid="capability-lifecycle"
        style="background: var(--highlighted-card-bg-color)"
      >
        <v-card-text class="px-0 py-2">
          <v-table density="compact" style="background: transparent">
            <tbody>
              <tr>
                <td class="font-weight-medium" style="width: 200px">
                  {{ $t('capabilityState') }}
                </td>
                <td>
                  <v-chip
                    data-testid="capability-state"
                    :color="capabilityStateColor(lifecycleDecision.state)"
                    size="small"
                    dark
                  >
                    {{ lifecycleDecision.state }}
                  </v-chip>
                </td>
              </tr>
              <tr>
                <td class="font-weight-medium">{{ $t('capabilityReason') }}</td>
                <td><code data-testid="capability-reason">{{ lifecycleDecision.reason }}</code></td>
              </tr>
            </tbody>
          </v-table>
        </v-card-text>
      </v-card>
    </template>

    <v-list-subheader class="px-0 mt-2">Structured file logs</v-list-subheader>
    <v-card
      v-if="structuredLogs"
      data-testid="structured-logs"
      style="background: var(--highlighted-card-bg-color)"
    >
      <v-card-text class="px-0 py-2">
        <v-table density="compact" style="background: transparent">
          <tbody>
            <tr>
              <td class="font-weight-medium" style="width: 200px">Writer state</td>
              <td>
                <v-chip
                  data-testid="structured-logs-state"
                  :color="structuredLogStateColor(structuredLogs.state)"
                  size="small"
                  dark
                >
                  {{ structuredLogs.state }}
                </v-chip>
                <span class="ml-2 text--secondary">
                  {{ structuredLogStateText(structuredLogs.state) }}
                </span>
              </td>
            </tr>
            <tr>
              <td class="font-weight-medium">Queue</td>
              <td data-testid="structured-logs-queue">
                {{ structuredLogs.queue_depth }} / {{ structuredLogs.queue_capacity }} records
              </td>
            </tr>
            <tr>
              <td class="font-weight-medium">Dropped records</td>
              <td data-testid="structured-logs-drops">{{ structuredLogs.dropped_records }}</td>
            </tr>
            <tr>
              <td class="font-weight-medium">Flush interval</td>
              <td>{{ structuredLogs.flush_interval || '—' }}</td>
            </tr>
            <tr>
              <td class="font-weight-medium">Rotation interval</td>
              <td>{{ structuredLogs.rotation_interval || '—' }}</td>
            </tr>
            <tr>
              <td class="font-weight-medium">Last successful flush</td>
              <td data-testid="structured-logs-last-flush">
                {{ formatStructuredLogTime(structuredLogs.last_successful_flush) }}
              </td>
            </tr>
          </tbody>
        </v-table>

        <v-alert
          v-if="structuredLogs.state === 'dropping'"
          data-testid="structured-logs-dropping"
          class="mx-4 mt-3 mb-1"
          type="warning"
          density="compact"
          variant="outlined"
        >
          The writer queue is full. New log records are being dropped without blocking task
          execution.
        </v-alert>
        <v-alert
          v-if="structuredLogs.state === 'failed'"
          data-testid="structured-logs-failed"
          class="mx-4 mt-3 mb-1"
          type="error"
          density="compact"
          variant="outlined"
        >
          {{ structuredLogFailureText(structuredLogs) }}
        </v-alert>
        <v-alert
          v-if="structuredLogs.state === 'disabled'"
          data-testid="structured-logs-disabled"
          class="mx-4 mt-3 mb-1"
          type="info"
          density="compact"
          variant="outlined"
        >
          Structured file logging is disabled. Normal task execution is unaffected.
        </v-alert>

        <v-table
          v-if="structuredLogs.destinations && structuredLogs.destinations.length"
          data-testid="structured-logs-destinations"
          density="compact"
          class="mt-2"
          style="background: transparent"
        >
          <thead><tr><th>Category</th><th>Destination</th><th>Retention</th></tr></thead>
          <tbody>
            <tr v-for="destination in structuredLogs.destinations" :key="destination.category">
              <td>{{ destination.category }}</td>
              <td class="structured-log-path">{{ destination.filename }}</td>
              <td>{{ structuredLogRetention(destination) }}</td>
            </tr>
          </tbody>
        </v-table>
      </v-card-text>
    </v-card>

    <template v-if="debugFilter">
      <v-list-subheader class="px-0 mt-2">Debug log filtering</v-list-subheader>
      <v-card data-testid="debug-filter" style="background: var(--highlighted-card-bg-color)">
        <v-card-text class="px-0 py-2">
          <v-table density="compact" style="background: transparent">
            <tbody>
              <tr>
                <td class="font-weight-medium" style="width: 200px">Instance</td>
                <td data-testid="debug-filter-instance">
                  <code>{{ debugFilter.instance || '—' }}</code>
                </td>
              </tr>
              <tr>
                <td class="font-weight-medium">Configured</td>
                <td>
                  <template v-if="debugFilter.configured && debugFilter.configured.length">
                    <v-chip
                      v-for="entry in debugFilter.configured"
                      :key="`configured-${entry}`"
                      class="mr-1 my-1"
                      size="small"
                    >{{ entry }}</v-chip>
                  </template>
                  <span v-else data-testid="debug-filter-empty" class="text--secondary">
                    {{ debugFilterDefaultText(debugFilter) }}
                  </span>
                </td>
              </tr>
              <tr>
                <td class="font-weight-medium">Effective</td>
                <td>
                  <template v-if="debugFilter.effective && debugFilter.effective.length">
                    <v-chip
                      v-for="entry in debugFilter.effective"
                      :key="`effective-${entry}`"
                      data-testid="debug-filter-effective"
                      class="mr-1 my-1"
                      color="primary"
                      variant="outlined"
                      size="small"
                    >{{ entry }}</v-chip>
                  </template>
                  <span v-else data-testid="debug-filter-none" class="text--secondary">
                    No components are captured.
                  </span>
                </td>
              </tr>
              <tr>
                <td class="font-weight-medium">Last reload</td>
                <td data-testid="debug-filter-reloaded-at">
                  {{ formatStructuredLogTime(debugFilter.reloaded_at) }}
                </td>
              </tr>
            </tbody>
          </v-table>

          <v-alert
            v-if="debugFilter.rejected && debugFilter.rejected.length"
            data-testid="debug-filter-rejected"
            class="mx-4 mt-3 mb-1"
            type="warning"
            density="compact"
            variant="outlined"
          >
            Rejected entries keep the filter narrow:
            <div v-for="entry in debugFilter.rejected" :key="entry.entry">
              <code>{{ debugFilterRejectedText(entry) }}</code>
            </div>
          </v-alert>
          <v-alert
            v-if="debugFilter.reload_error"
            data-testid="debug-filter-reload-error"
            class="mx-4 mt-3 mb-1"
            type="error"
            density="compact"
            variant="outlined"
          >
            Reload failed; the last-known-good filter remains active.
            <div><code>{{ debugFilter.reload_error }}</code></div>
          </v-alert>
        </v-card-text>
      </v-card>
    </template>
  </div>
</template>

<script>
import { capabilityStateColor, findCapabilityDecision } from '@/lib/capabilities';

export default {
  props: {
    diagnostics: Object,
    systemInfo: Object,
  },
  computed: {
    lifecycleDecision() { return findCapabilityDecision(this.systemInfo, 'lifecycle_test'); },
    structuredLogs() { return this.diagnostics?.structured_logs || null; },
    debugFilter() { return this.diagnostics?.debug_filter || null; },
  },
  methods: {
    capabilityStateColor,
    structuredLogStateColor(state) {
      return {
        healthy: 'success', disabled: 'grey', dropping: 'warning', failed: 'error',
      }[state]
        || 'grey';
    },
    structuredLogStateText(state) {
      return {
        healthy: 'All configured destinations are writable.',
        disabled: 'No structured log destinations are active.',
        dropping: 'The bounded queue has discarded records.',
        failed: 'A destination or flush operation failed.',
      }[state] || 'Writer state is unknown.';
    },
    structuredLogRetention(destination) {
      const parts = [];
      if (destination.max_size_megabytes) parts.push(`${destination.max_size_megabytes} MB`);
      if (destination.max_age_days) {
        parts.push(`${destination.max_age_days} ${destination.max_age_days === 1 ? 'day' : 'days'}`);
      }
      if (destination.max_backups) {
        parts.push(`${destination.max_backups} ${destination.max_backups === 1 ? 'backup' : 'backups'}`);
      }
      if (destination.compress) parts.push('gzip');
      return parts.join(' · ') || 'unlimited';
    },
    structuredLogFailureText(diagnostics) {
      return diagnostics.last_write_error || 'The writer could not access its destination.';
    },
    formatStructuredLogTime(value) {
      if (!value) return 'Never';
      const parsed = new Date(value);
      return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
    },
    debugFilterDefaultText(diagnostics) {
      return diagnostics.default === 'all'
        ? 'All components are captured by default.'
        : 'No component filters are configured.';
    },
    debugFilterRejectedText(entry) {
      return `${entry.entry} — ${entry.reason.replace(/_/g, ' ')}`;
    },
  },
};
</script>

<style scoped>
.structured-log-path {
  font-family: monospace;
  overflow-wrap: anywhere;
}
</style>
