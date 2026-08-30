<template>
  <v-dialog v-model="dialog" max-width="900" scrollable>
    <v-card data-testid="runner-health-dialog">
      <v-card-title class="headline RunnerHealthDialog__title">
        <div class="RunnerHealthDialog__titleText">
          <div>{{ dialogTitle }}</div>
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

      <v-card-text v-else-if="health || dockerAdmin" class="py-2 mb-4">
        <v-subheader v-if="health" class="px-0">{{ $t('runnerHealth') }}</v-subheader>
        <v-card
          v-if="health"
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

        <template v-if="dockerAdmin">
          <v-subheader class="px-0 mt-3">Docker execution policy</v-subheader>
          <v-card
            data-testid="runner-docker-policy-panel"
            style="background: var(--highlighted-card-bg-color)"
          >
            <v-card-text>
              <v-alert v-if="dockerPolicyError" type="error" dense text>
                {{ dockerPolicyError }}
              </v-alert>
              <template v-if="dockerPolicy">
                <div class="RunnerHealthDialog__dockerGrid">
                  <div><strong>Revision:</strong> {{ dockerPolicy.revision }}</div>
                  <div>
                    <strong>Runner acknowledgement:</strong>
                    <v-chip
                      small
                      :color="dockerPolicyAcknowledged ? 'success' : 'warning'"
                      :text-color="dockerPolicyAcknowledged ? 'white' : undefined"
                      data-testid="runner-docker-policy-ack"
                    >
                      {{ dockerPolicyAcknowledged ? 'Current' : 'Pending' }}
                    </v-chip>
                  </div>
                  <div><strong>Network:</strong> {{ dockerPolicy.network }}</div>
                  <div><strong>User:</strong> <code>{{ dockerPolicy.user }}</code></div>
                  <div><strong>Limits:</strong> {{ dockerPolicyLimits }}</div>
                  <div>
                    <strong>Profiles:</strong>
                    {{ dockerPolicy.seccomp_profile }} / {{ dockerPolicy.apparmor_profile }}
                  </div>
                </div>
                <div class="mt-2">
                  <strong>Allowed image digests:</strong>
                  <div v-if="dockerPolicy.allowed_images?.length" class="mt-1">
                    <code
                      v-for="image in dockerPolicy.allowed_images"
                      :key="image"
                      class="RunnerHealthDialog__imageDigest"
                    >{{ image }}</code>
                  </div>
                  <span v-else class="warning--text">No Docker image is currently allowed.</span>
                </div>
                <v-btn
                  class="mt-3"
                  small
                  outlined
                  color="primary"
                  data-testid="runner-docker-policy-edit"
                  @click="beginDockerPolicyEdit"
                >
                  {{ editingDockerPolicy ? 'Reset policy form' : 'Edit policy' }}
                </v-btn>
              </template>

              <v-expand-transition>
                <div v-if="editingDockerPolicy && dockerPolicyDraft" class="mt-4">
                  <v-textarea
                    v-model="dockerPolicyDraft.allowedImagesText"
                    label="Allowed immutable image digests (one per line)"
                    rows="3"
                    outlined
                    dense
                    hide-details="auto"
                  />
                  <v-row class="mt-1">
                    <v-col cols="12" sm="6">
                      <v-text-field v-model="dockerPolicyDraft.network" label="Network" dense />
                    </v-col>
                    <v-col cols="12" sm="6">
                      <v-text-field v-model="dockerPolicyDraft.user" label="Container user" dense />
                    </v-col>
                    <v-col cols="12" sm="4">
                      <v-text-field
                        v-model.number="dockerPolicyDraft.nano_cpus"
                        type="number"
                        label="Nano CPUs"
                        dense
                      />
                    </v-col>
                    <v-col cols="12" sm="4">
                      <v-text-field
                        v-model.number="dockerPolicyDraft.memory_bytes"
                        type="number"
                        label="Memory bytes"
                        dense
                      />
                    </v-col>
                    <v-col cols="12" sm="4">
                      <v-text-field
                        v-model.number="dockerPolicyDraft.pids_limit"
                        type="number"
                        label="PID limit"
                        dense
                      />
                    </v-col>
                    <v-col cols="12" sm="6">
                      <v-text-field
                        v-model.number="dockerPolicyDraft.pull_timeout_seconds"
                        type="number"
                        label="Pull timeout seconds"
                        dense
                      />
                    </v-col>
                    <v-col cols="12" sm="6">
                      <v-text-field
                        v-model.number="dockerPolicyDraft.max_image_size_bytes"
                        type="number"
                        label="Maximum image bytes"
                        dense
                      />
                    </v-col>
                  </v-row>
                  <v-btn
                    color="primary"
                    small
                    :loading="savingDockerPolicy"
                    data-testid="runner-docker-policy-save"
                    @click="saveDockerPolicy"
                  >
                    Save policy
                  </v-btn>
                  <v-btn small text class="ml-2" @click="editingDockerPolicy = false">
                    {{ $t('cancel') }}
                  </v-btn>
                </div>
              </v-expand-transition>
            </v-card-text>
          </v-card>

          <v-subheader class="px-0 mt-3">Docker quarantine</v-subheader>
          <v-card
            data-testid="runner-docker-diagnostics-panel"
            style="background: var(--highlighted-card-bg-color)"
          >
            <v-card-text>
              <v-alert v-if="dockerDiagnosticsError" type="error" dense text>
                {{ dockerDiagnosticsError }}
              </v-alert>
              <div v-if="dockerDiagnostics.length">
                <div
                  v-for="diagnostic in dockerDiagnostics"
                  :key="dockerDiagnosticKey(diagnostic)"
                  class="RunnerHealthDialog__diagnostic"
                  data-testid="runner-docker-diagnostic"
                >
                  <div>
                    <strong>{{ dockerDiagnosticTitle(diagnostic) }}</strong>
                    <div class="text--secondary text-body-2">
                      {{ diagnostic.observed_at | formatDate }}
                    </div>
                  </div>
                  <v-btn
                    small
                    color="warning"
                    :loading="remediatingDockerDiagnostics.includes(
                      dockerDiagnosticKey(diagnostic)
                    )"
                    @click="requestDockerRemediation(diagnostic)"
                  >
                    Retry safe cleanup
                  </v-btn>
                </div>
              </div>
              <div v-else class="text--secondary">No pending Docker quarantine.</div>
            </v-card-text>
            <v-card-actions v-if="dockerDiagnosticsNextCursor">
              <v-spacer />
              <v-btn text color="primary" @click="loadDockerDiagnostics(true)">
                Load more
              </v-btn>
            </v-card-actions>
          </v-card>
        </template>

        <v-subheader v-if="projectId" class="px-0 mt-3">
          {{ $t('runnerAssignmentHistory') }}
        </v-subheader>
        <v-card
          v-if="projectId"
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
      dockerPolicy: null,
      dockerPolicyDraft: null,
      dockerPolicyError: null,
      editingDockerPolicy: false,
      savingDockerPolicy: false,
      dockerDiagnostics: [],
      dockerDiagnosticsNextCursor: null,
      dockerDiagnosticsError: null,
      remediatingDockerDiagnostics: [],
      dockerRemediationKeys: {},
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

    dockerAdmin() {
      return this.projectId == null
        && this.runner?.project_id == null
        && this.runner?.executor_type === 'docker';
    },

    dialogTitle() {
      return this.dockerAdmin ? 'Docker runner diagnostics' : this.$t('runnerHealthAndHistory');
    },

    dockerPolicyAcknowledged() {
      return this.dockerPolicy != null
        && this.runner?.docker_policy_revision === this.dockerPolicy.revision
        && this.runner?.docker_policy_hash === this.dockerPolicy.hash;
    },

    dockerPolicyLimits() {
      if (!this.dockerPolicy) return '—';
      const cpu = Number(this.dockerPolicy.nano_cpus || 0) / 1_000_000_000;
      const memory = Math.round(Number(this.dockerPolicy.memory_bytes || 0) / 1024 / 1024);
      return `${cpu} CPU · ${memory} MiB · ${this.dockerPolicy.pids_limit} PIDs`;
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
      if (!this.runner || (!this.projectId && !this.dockerAdmin)) {
        return;
      }
      this.loading = true;
      this.error = null;
      try {
        if (this.dockerAdmin) {
          this.health = null;
          this.history = [];
          this.hasMore = false;
          this.nextBefore = null;
          await Promise.all([
            this.loadDockerPolicy(),
            this.loadDockerDiagnostics(false),
          ]);
          return;
        }
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

    async loadDockerPolicy() {
      this.dockerPolicyError = null;
      try {
        const { data } = await axios.get('/api/runners/docker-policy');
        this.dockerPolicy = data;
      } catch (error) {
        this.dockerPolicy = null;
        this.dockerPolicyError = getErrorMessage(error);
      }
    },

    beginDockerPolicyEdit() {
      if (!this.dockerPolicy) return;
      this.dockerPolicyDraft = {
        ...JSON.parse(JSON.stringify(this.dockerPolicy)),
        allowedImagesText: (this.dockerPolicy.allowed_images || []).join('\n'),
      };
      this.editingDockerPolicy = true;
    },

    async saveDockerPolicy() {
      if (!this.dockerPolicyDraft || this.savingDockerPolicy) return;
      this.savingDockerPolicy = true;
      this.dockerPolicyError = null;
      try {
        const payload = { ...this.dockerPolicyDraft };
        payload.allowed_images = payload.allowedImagesText
          .split('\n')
          .map((value) => value.trim())
          .filter(Boolean);
        payload.allowed_networks = [...new Set([
          ...(payload.allowed_networks || []),
          'none',
          payload.network,
        ].filter(Boolean))];
        delete payload.allowedImagesText;
        const { data } = await axios.put('/api/runners/docker-policy', payload);
        this.dockerPolicy = data;
        this.dockerPolicyDraft = null;
        this.editingDockerPolicy = false;
      } catch (error) {
        this.dockerPolicyError = getErrorMessage(error);
      } finally {
        this.savingDockerPolicy = false;
      }
    },

    async loadDockerDiagnostics(append = false) {
      if (!this.runner) return;
      this.dockerDiagnosticsError = null;
      try {
        const params = { limit: 25 };
        if (append && this.dockerDiagnosticsNextCursor) {
          params.cursor = this.dockerDiagnosticsNextCursor;
        }
        const { data } = await axios.get(
          `/api/runners/${this.runner.id}/docker-reconciliation/diagnostics`,
          { params },
        );
        this.dockerDiagnostics = append
          ? [...this.dockerDiagnostics, ...(data.diagnostics || [])]
          : (data.diagnostics || []);
        this.dockerDiagnosticsNextCursor = data.next_cursor || null;
      } catch (error) {
        this.dockerDiagnosticsError = getErrorMessage(error);
      }
    },

    dockerDiagnosticKey(diagnostic) {
      const remediation = diagnostic.remediation || {};
      if (remediation.target === 'candidate') {
        return `candidate:${remediation.candidate?.session_id}:${remediation.candidate?.fingerprint}`;
      }
      const key = remediation.quarantine || {};
      return `quarantine:${key.runner_boot}:${key.project_id}:${key.task_id}:${key.generation}:${key.resource}`;
    },

    dockerDiagnosticTitle(diagnostic) {
      if (diagnostic.type === 'candidate') {
        return `${diagnostic.candidate?.resource || 'resource'} · ${diagnostic.candidate?.name || 'unnamed'} · ${diagnostic.candidate?.reason || 'unknown'}`;
      }
      const quarantine = diagnostic.quarantine || {};
      return `${quarantine.resource || 'resource'} · task #${quarantine.task_id || '—'} · ${quarantine.state || 'quarantined'}`;
    },

    dockerRemediationIdempotencyKey(diagnostic) {
      const diagnosticKey = this.dockerDiagnosticKey(diagnostic);
      if (!this.dockerRemediationKeys[diagnosticKey]) {
        const random = window.crypto?.randomUUID?.()
          || `${Date.now()}-${Math.random().toString(36).slice(2)}`;
        this.$set(this.dockerRemediationKeys, diagnosticKey, `ui-${random}`);
      }
      return this.dockerRemediationKeys[diagnosticKey];
    },

    async requestDockerRemediation(diagnostic) {
      const diagnosticKey = this.dockerDiagnosticKey(diagnostic);
      if (this.remediatingDockerDiagnostics.includes(diagnosticKey)) return;
      this.remediatingDockerDiagnostics.push(diagnosticKey);
      this.dockerDiagnosticsError = null;
      const remediation = diagnostic.remediation;
      const payload = {
        action: 'retry_stop_and_cleanup',
        idempotency_key: this.dockerRemediationIdempotencyKey(diagnostic),
        expected_revision: remediation.expected_revision,
        target: remediation.target,
      };
      if (remediation.target === 'candidate') {
        payload.session_id = remediation.candidate.session_id;
        payload.fingerprint = remediation.candidate.fingerprint;
      } else {
        payload.quarantine = remediation.quarantine;
      }
      try {
        await axios.post(
          `/api/runners/${this.runner.id}/docker-reconciliation/remediation`,
          payload,
        );
        this.$delete(this.dockerRemediationKeys, diagnosticKey);
        await this.loadDockerDiagnostics(false);
      } catch (error) {
        this.dockerDiagnosticsError = getErrorMessage(error);
      } finally {
        this.remediatingDockerDiagnostics = this.remediatingDockerDiagnostics
          .filter((key) => key !== diagnosticKey);
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

.RunnerHealthDialog__dockerGrid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px 16px;
}

.RunnerHealthDialog__imageDigest {
  display: block;
  overflow-wrap: anywhere;
  margin-top: 4px;
}

.RunnerHealthDialog__diagnostic {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 12px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.RunnerHealthDialog__diagnostic:first-child {
  border-top: 0;
  padding-top: 0;
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

  .RunnerHealthDialog__dockerGrid {
    grid-template-columns: 1fr;
  }

  .RunnerHealthDialog__diagnostic {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
