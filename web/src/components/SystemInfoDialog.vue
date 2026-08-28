<template>
  <v-dialog v-model="dialog" max-width="700" scrollable>
    <v-card>
      <v-card-title class="headline">
        {{ $t('systemInfo') }}
        <v-spacer />
        <v-btn icon @click="dialog = false">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-card-text v-if="loading" class="text-center py-8">
        <v-progress-circular indeterminate color="primary" />
      </v-card-text>

      <v-card-text v-else-if="error" class="py-4">
        <v-alert type="error" dense outlined>
          {{ error }}
        </v-alert>
      </v-card-text>

      <v-card-text v-else-if="info" class="py-2 mb-4">
        <!-- System -->
        <v-subheader class="px-0">{{ $t('system') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('version') }}
                  </td>
                  <td>{{ info.system.version }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('goVersion') }}</td>
                  <td>
                    {{ info.system.go_version }}
                    ({{ info.system.go_os }}/{{ info.system.go_arch }})
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('gitClient') }}</td>
                  <td>{{ info.system.git_client }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('tmpPath') }}</td>
                  <td>{{ info.system.tmp_path }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('homeDirMode') }}</td>
                  <td>{{ info.system.home_dir_mode }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('scheduleTimezone') }}</td>
                  <td>{{ info.system.schedule_timezone }}</td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

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
                    <td>
                      <code data-testid="capability-reason">{{ lifecycleDecision.reason }}</code>
                    </td>
                  </tr>
                </tbody>
              </v-simple-table>
            </v-card-text>
          </v-card>
        </template>

        <template v-if="totpDecision && totpDecision.state !== 'unavailable'">
          <v-subheader class="px-0 mt-2">TOTP rollout</v-subheader>
          <v-card
            data-testid="totp-rollout"
            style="background: var(--highlighted-card-bg-color)"
          >
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
              <v-alert
                v-if="totpRollout.state === 'required'"
                type="warning"
                dense
                outlined
              >
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
                  <tr>
                    <th>Transition</th>
                    <th>Actor</th>
                    <th>Time</th>
                  </tr>
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
                  <td data-testid="structured-logs-drops">
                    {{ structuredLogs.dropped_records }}
                  </td>
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
              <thead>
                <tr>
                  <th>Category</th>
                  <th>Destination</th>
                  <th>Retention</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="destination in structuredLogs.destinations" :key="destination.category">
                  <td>{{ destination.category }}</td>
                  <td class="structured-log-path">{{ destination.filename }}</td>
                  <td>
                    {{ structuredLogRetention(destination) }}
                  </td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <template v-if="debugFilter">
          <v-subheader class="px-0 mt-2">Debug log filtering</v-subheader>
          <v-card
            data-testid="debug-filter"
            style="background: var(--highlighted-card-bg-color)"
          >
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
                        >
                          {{ entry }}
                        </v-chip>
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
                        >
                          {{ entry }}
                        </v-chip>
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

        <!-- Ansible -->
        <v-subheader class="px-0 mt-2">Ansible</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="py-2">
            <pre v-if="info.system.ansible" class="ansible-version">
              {{ info.system.ansible.trim() }}
            </pre>
            <div v-else class="px-0 text--secondary text-body-2">
              {{ $t('ansibleNotFound') }}
            </div>
          </v-card-text>
        </v-card>

        <!-- Database -->
        <v-subheader class="px-0 mt-2">{{ $t('database') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('dbDialect') }}
                  </td>
                  <td>{{ info.database.dialect }}</td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <!-- Authentication -->
        <v-subheader class="px-0 mt-2">{{ $t('authentication') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('passwordLogin') }}
                  </td>
                  <td>
                    <v-icon small :color="info.auth.password_login_enabled ? 'success' : 'grey'">
                      {{
                        info.auth.password_login_enabled ? 'mdi-check-circle' : 'mdi-close-circle'
                      }}
                    </v-icon>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">TOTP</td>
                  <td>
                    <v-icon small :color="info.auth.totp_enabled ? 'success' : 'grey'">
                      {{ info.auth.totp_enabled ? 'mdi-check-circle' : 'mdi-close-circle' }}
                    </v-icon>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('emailOtp') }}</td>
                  <td>
                    <v-icon small :color="info.auth.email_otp_enabled ? 'success' : 'grey'">
                      {{
                        info.auth.email_otp_enabled ? 'mdi-check-circle' : 'mdi-close-circle'
                      }}
                    </v-icon>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">LDAP</td>
                  <td>
                    <v-icon small :color="info.auth.ldap_enabled ? 'success' : 'grey'">
                      {{ info.auth.ldap_enabled ? 'mdi-check-circle' : 'mdi-close-circle' }}
                    </v-icon>
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('oidcProviders') }}</td>
                  <td>
                    <span v-if="info.auth.oidc_providers && info.auth.oidc_providers.length > 0">
                      {{ info.auth.oidc_providers.join(', ') }}
                    </span>
                    <span v-else class="text--secondary">{{ $t('none') }}</span>
                  </td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <!-- Notifications -->
        <v-subheader class="px-0 mt-2">{{ $t('notifications') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr v-for="(enabled, name) in info.notifications" :key="name">
                  <td class="font-weight-medium" style="width: 200px; text-transform: capitalize">
                    {{ formatNotificationName(name) }}
                  </td>
                  <td>
                    <v-icon small :color="enabled ? 'success' : 'grey'">
                      {{ enabled ? 'mdi-check-circle' : 'mdi-close-circle' }}
                    </v-icon>
                  </td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <v-subheader class="px-0 mt-2">{{ $t('cluster') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('highAvailability') }}
                  </td>
                  <td>
                    <v-icon small :color="info.cluster.ha_enabled ? 'success' : 'grey'">
                      {{ info.cluster.ha_enabled ? 'mdi-check-circle' : 'mdi-close-circle' }}
                    </v-icon>
                  </td>
                </tr>
                <tr v-if="info.cluster.ha_enabled">
                  <td class="font-weight-medium">{{ $t('nodeId') }}</td>
                  <td>{{ info.cluster.node_id }}</td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <!-- Runners -->
        <v-subheader class="px-0 mt-2">{{ $t('runners') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('useRemoteRunner') }}
                  </td>
                  <td>
                    <v-icon small :color="info.runners.use_remote_runner ? 'success' : 'grey'">
                      {{ info.runners.use_remote_runner ? 'mdi-check-circle' : 'mdi-close-circle' }}
                    </v-icon>
                  </td>
                </tr>
                <tr v-if="info.runners.total != null">
                  <td class="font-weight-medium">{{ $t('globalRunners') }}</td>
                  <td>{{ info.runners.active }} / {{ info.runners.total }} {{ $t('active2') }}</td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <!-- Task Settings -->
        <v-subheader class="px-0 mt-2">{{ $t('taskSettings') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('maxParallelTasks') }}
                  </td>
                  <td>{{ info.task_settings.max_parallel_tasks || $t('unlimited') }}</td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('maxTaskDuration') }}</td>
                  <td>
                    {{
                      info.task_settings.max_task_duration_sec
                        ? info.task_settings.max_task_duration_sec + 's'
                        : $t('unlimited')
                    }}
                  </td>
                </tr>
                <tr>
                  <td class="font-weight-medium">{{ $t('maxTasksPerTemplate') }}</td>
                  <td>{{ info.task_settings.max_tasks_per_template || $t('unlimited') }}</td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>

        <!-- Features -->
        <v-subheader class="px-0 mt-2">{{ $t('featureFlags') }}</v-subheader>
        <v-card style="background: var(--highlighted-card-bg-color)">
          <v-card-text class="px-0 py-2">
            <v-simple-table dense style="background: transparent">
              <tbody>
                <tr>
                  <td class="font-weight-medium" style="width: 200px">
                    {{ $t('nonAdminCanCreateProject') }}
                  </td>
                  <td>
                    <v-icon
                      small
                      :color="info.features.non_admin_can_create_project ? 'success' : 'grey'"
                    >
                      {{
                        info.features.non_admin_can_create_project
                          ? 'mdi-check-circle'
                          : 'mdi-close-circle'
                      }}
                    </v-icon>
                  </td>
                </tr>
              </tbody>
            </v-simple-table>
          </v-card-text>
        </v-card>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<style scoped>
.ansible-version {
  font-size: 12px;
  margin: 0;
  font-family: monospace;
  overflow-x: auto;
}

.structured-log-path {
  font-family: monospace;
  overflow-wrap: anywhere;
}
</style>

<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/system-info-dialog';

import axios from 'axios';

export default {
  props: {
    value: Boolean,
    systemInfo: Object,
  },

  data() {
    return {
      dialog: false,
      info: null,
      loading: false,
      error: null,
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

  watch: {
    async dialog(val) {
      this.$emit('input', val);
      if (val && !this.info) {
        await this.loadInfo();
      }
    },

    value(val) {
      this.dialog = val;
    },
  },

  computed: {
    ...enhancedComputed,

  },

  methods: {
    ...enhancedMethods,

    formatNotificationName(name) {
      return name.replace(/_/g, ' ');
    },

    async loadInfo() {
      this.loading = true;
      this.error = null;
      try {
        this.info = (
          await axios({
            method: 'get',
            url: '/api/admin/info',
            responseType: 'json',
          })
        ).data;
        await this.loadTotpRollout();
      } catch (err) {
        this.error = err.response?.data?.message || err.message || 'Failed to load system info';
      } finally {
        this.loading = false;
      }
    },
  },
};
</script>
