<template>
  <div>
    <template v-if="lifecycleDecision">
      <v-subheader class="px-0 mt-2">{{ $t('lifecycleCapability') }}</v-subheader>
      <v-card
        data-testid="capability-lifecycle"
        style="background: var(--highlighted-card-bg-color)"
      >
        <v-card-text class="px-0 py-2">
          <v-simple-table dense style="background: transparent">
            <tbody>
              <tr>
                <td class="font-weight-medium" style="width: 200px">
                  {{ $t('capabilityState') }}
                </td>
                <td>
                  <v-chip
                    data-testid="capability-state"
                    :color="capabilityStateColor(lifecycleDecision.state)"
                    small
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
          </v-simple-table>
        </v-card-text>
      </v-card>
    </template>

    <template v-if="totpDecision && totpDecision.state !== 'unavailable'">
      <v-subheader class="px-0 mt-2">TOTP rollout</v-subheader>
      <v-card data-testid="totp-rollout" style="background: var(--highlighted-card-bg-color)">
        <v-card-text>
          <v-alert v-if="totpRolloutError" type="error" dense outlined>
            {{ totpRolloutError }}
          </v-alert>
          <v-select
            v-model="totpRollout.state"
            data-testid="totp-rollout-state"
            :items="totpRolloutStates"
            label="Lifecycle state"
            outlined
            dense
          />
          <v-select
            v-if="totpRollout.state === 'required_selected'"
            v-model="totpRollout.selected_user_ids"
            data-testid="totp-selected-users"
            :items="totpUsers"
            item-text="username"
            item-value="id"
            label="Users required to enroll"
            multiple
            chips
            outlined
            dense
          />
          <v-alert v-if="totpRollout.state === 'required'" type="warning" dense outlined>
            Required mode is accepted only when every user can enroll and at least one local
            administrator has acknowledged unused recovery codes.
          </v-alert>
          <v-btn
            data-testid="totp-rollout-save"
            color="primary"
            :loading="totpRolloutSaving"
            @click="saveTotpRollout"
          >
            Apply TOTP rollout
          </v-btn>

          <v-simple-table v-if="totpTransitions.length" dense class="mt-4">
            <thead>
              <tr><th>Transition</th><th>Actor</th><th>Time</th></tr>
            </thead>
            <tbody>
              <tr v-for="transition in totpTransitions.slice(0, 8)" :key="transition.id">
                <td>{{ transition.from_state }} → {{ transition.to_state }}</td>
                <td>{{ transition.actor_id }}</td>
                <td>{{ new Date(transition.created).toLocaleString() }}</td>
              </tr>
            </tbody>
          </v-simple-table>
        </v-card-text>
      </v-card>
    </template>

    <template v-if="ldapDecision && ldapDecision.state !== 'unavailable'">
      <v-subheader class="px-0 mt-2">LDAP authentication</v-subheader>
      <LdapCapabilityPanel />
    </template>

    <v-subheader class="px-0 mt-2">Structured file logs</v-subheader>
    <v-card
      v-if="structuredLogs"
      data-testid="structured-logs"
      style="background: var(--highlighted-card-bg-color)"
    >
      <v-card-text class="px-0 py-2">
        <v-simple-table dense style="background: transparent">
          <tbody>
            <tr>
              <td class="font-weight-medium" style="width: 200px">Writer state</td>
              <td>
                <v-chip
                  data-testid="structured-logs-state"
                  :color="structuredLogStateColor(structuredLogs.state)"
                  small
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
        </v-simple-table>

        <v-alert
          v-if="structuredLogs.state === 'dropping'"
          data-testid="structured-logs-dropping"
          class="mx-4 mt-3 mb-1"
          type="warning"
          dense
          outlined
        >
          The writer queue is full. New log records are being dropped without blocking task
          execution.
        </v-alert>
        <v-alert
          v-if="structuredLogs.state === 'failed'"
          data-testid="structured-logs-failed"
          class="mx-4 mt-3 mb-1"
          type="error"
          dense
          outlined
        >
          {{ structuredLogFailureText(structuredLogs) }}
        </v-alert>
        <v-alert
          v-if="structuredLogs.state === 'disabled'"
          data-testid="structured-logs-disabled"
          class="mx-4 mt-3 mb-1"
          type="info"
          dense
          outlined
        >
          Structured file logging is disabled. Normal task execution is unaffected.
        </v-alert>

        <v-simple-table
          v-if="structuredLogs.destinations && structuredLogs.destinations.length"
          data-testid="structured-logs-destinations"
          dense
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
        </v-simple-table>
      </v-card-text>
    </v-card>

    <template v-if="debugFilter">
      <v-subheader class="px-0 mt-2">Debug log filtering</v-subheader>
      <v-card data-testid="debug-filter" style="background: var(--highlighted-card-bg-color)">
        <v-card-text class="px-0 py-2">
          <v-simple-table dense style="background: transparent">
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
                      small
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
                      outlined
                      small
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
          </v-simple-table>

          <v-alert
            v-if="debugFilter.rejected && debugFilter.rejected.length"
            data-testid="debug-filter-rejected"
            class="mx-4 mt-3 mb-1"
            type="warning"
            dense
            outlined
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
            dense
            outlined
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
import axios from 'axios';
import LdapCapabilityPanel from '@/components/LdapCapabilityPanel.vue';
import { capabilityStateColor, findCapabilityDecision } from '@/lib/capabilities';

export default {
  components: { LdapCapabilityPanel },
  props: {
    diagnostics: Object,
    systemInfo: Object,
  },
  data() {
    return {
      totpRollout: { state: 'disabled', selected_user_ids: [] },
      totpRolloutStates: [
        { text: 'Disabled', value: 'disabled' },
        { text: 'Shadow', value: 'shadow' },
        { text: 'Optional', value: 'optional' },
        { text: 'Required for selected users', value: 'required_selected' },
        { text: 'Required for everyone', value: 'required' },
      ],
      totpUsers: [],
      totpTransitions: [],
      totpRolloutSaving: false,
      totpRolloutError: null,
    };
  },
  computed: {
    lifecycleDecision() { return findCapabilityDecision(this.systemInfo, 'lifecycle_test'); },
    totpDecision() { return findCapabilityDecision(this.systemInfo, 'totp'); },
    ldapDecision() { return findCapabilityDecision(this.systemInfo, 'ldap'); },
    structuredLogs() { return this.diagnostics?.structured_logs || null; },
    debugFilter() { return this.diagnostics?.debug_filter || null; },
  },
  created() {
    this.loadTotpRollout();
  },
  methods: {
    capabilityStateColor,
    async loadTotpRollout() {
      if (!this.totpDecision || this.totpDecision.state === 'unavailable') return;
      this.totpRolloutError = null;
      try {
        const [configuration, users, transitions] = await Promise.all([
          axios.get('/api/capabilities/totp'),
          axios.get('/api/users'),
          axios.get('/api/capabilities/totp/transitions'),
        ]);
        this.totpRollout = configuration.data;
        this.totpUsers = (users.data || []).filter((user) => !user.external);
        this.totpTransitions = transitions.data || [];
      } catch (error) {
        this.totpRolloutError = error.response?.data?.error || error.message;
      }
    },
    async saveTotpRollout() {
      this.totpRolloutSaving = true;
      this.totpRolloutError = null;
      try {
        await axios.put('/api/capabilities/totp', this.totpRollout);
        await this.loadTotpRollout();
        this.$emit('totp-rollout-updated');
      } catch (error) {
        this.totpRolloutError = error.response?.data?.error || error.message;
      } finally {
        this.totpRolloutSaving = false;
      }
    },
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
