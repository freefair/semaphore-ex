<template>
  <div class="pb-3">
    <v-alert
      v-if="item.recovery_reason"
      type="warning"
      outlined
      class="mb-4"
      data-testid="task-recovery-reason"
    >
      <strong>Runner recovery:</strong> {{ item.recovery_reason }}
    </v-alert>

    <v-alert
      v-if="recoveryDiagnostics"
      :type="recoveryDiagnostics.quarantined ? 'warning' : 'info'"
      outlined
      class="mb-4 TaskRunnerDetails__recoveryAlert"
      data-testid="task-recovery-diagnostics"
    >
      <div class="TaskRunnerDetails__recoveryHeader">
        <strong>HA task recovery</strong>
        <v-chip
          v-if="recoveryDiagnostics.recovery_decision"
          small
          label
          :color="recoveryDecisionColor(recoveryDiagnostics.recovery_decision)"
          text-color="white"
        >
          {{ recoveryDiagnostics.recovery_decision }}
        </v-chip>
      </div>
      <div class="TaskRunnerDetails__recoveryGrid mt-2">
        <div><strong>Owner:</strong> <code>{{ recoveryDiagnostics.owner_boot_id }}</code></div>
        <div v-if="recoveryDiagnostics.previous_owner_boot_id">
          <strong>Previous owner:</strong>
          <code>{{ recoveryDiagnostics.previous_owner_boot_id }}</code>
        </div>
        <div><strong>Fence:</strong> {{ recoveryDiagnostics.fencing_token }}</div>
        <div>
          <strong>Execution:</strong>
          runner #{{ recoveryDiagnostics.runner_id }}, attempt
          #{{ recoveryDiagnostics.assignment_generation }}
        </div>
        <div>
          <strong>Evidence:</strong> {{ recoveryDiagnostics.evidence_state }}
          <span v-if="recoveryDiagnostics.evidence_terminal_status">
            · {{ recoveryDiagnostics.evidence_terminal_status }}
          </span>
        </div>
        <div v-if="recoveryDiagnostics.evidence_observed_at">
          <strong>Observed:</strong>
          {{ recoveryDiagnostics.evidence_observed_at | formatDate }}
        </div>
      </div>
      <div v-if="recoveryDiagnostics.recovery_reason" class="mt-2">
        {{ recoveryDiagnostics.recovery_reason }}
      </div>
      <v-btn
        v-if="recoveryDiagnostics.safe_action === 'retry_recovery'"
        class="mt-3"
        color="warning"
        small
        :loading="retryingRecovery"
        @click="retryTaskRecovery"
      >
        Retry safe recovery check
      </v-btn>
      <div v-if="recoveryActionError" class="mt-2 error--text">
        {{ recoveryActionError }}
      </div>
    </v-alert>

    <v-alert
      v-else-if="item.recovery_reason && recoveryDiagnosticsError"
      type="error"
      dense
      text
      class="mb-4"
    >
      {{ recoveryDiagnosticsError }}
    </v-alert>

    <v-alert
      v-if="placementDecision"
      :type="placementRejected ? 'warning' : 'info'"
      outlined
      class="mb-4"
      data-testid="task-placement-decision"
    >
      <strong>{{ placementRejected ? 'Waiting for runner:' : 'Runner placement:' }}</strong>
      {{ placementDecision.reason }}
      <div v-if="placementDecision.action_hint" class="mt-1">
        {{ placementDecision.action_hint }}
      </div>
    </v-alert>

    <slot />

    <v-row v-if="credentialUsage.length > 0 || credentialUsageError">
      <v-col cols="12">
        <v-card
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
          class="mb-5"
          data-testid="task-credential-usage"
        >
          <v-card-title>Credential provenance</v-card-title>
          <v-card-text>
            <v-alert v-if="credentialUsageError" type="error" dense text class="mb-0">
              {{ credentialUsageError }}
            </v-alert>
            <div
              v-for="usage in credentialUsage"
              :key="usage.id"
              class="TaskRunnerDetails__credentialUsage"
            >
              <div>
                <strong><code>{{ usage.snapshot.target }}</code></strong>
                · credential #{{ usage.snapshot.credential_id }}
              </div>
              <div>
                <span v-if="usage.snapshot.credential_version">
                  version {{ usage.snapshot.credential_version }} ·
                  <code>
                    {{ shortCredentialFingerprint(usage.snapshot.version_fingerprint) }}
                  </code> ·
                </span>
                <v-chip x-small :color="credentialOutcomeColor(usage.snapshot.outcome)">
                  {{ usage.snapshot.outcome }}
                </v-chip>
                <span class="ml-2">{{ usage.snapshot.reason }}</span>
              </div>
            </div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-row v-if="runnerAttempts.length > 0 || runnerAttemptsError">
      <v-col cols="12">
        <v-card
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
          class="mb-5"
        >
          <v-card-title>Runner attempts</v-card-title>
          <v-card-text>
            <v-alert v-if="runnerAttemptsError" type="error" dense text class="mb-0">
              {{ runnerAttemptsError }}
            </v-alert>
            <div
              v-for="attempt in runnerAttempts"
              :key="attempt.generation"
              class="TaskRunnerDetails__attempt"
              data-testid="task-runner-attempt"
            >
              <div class="TaskRunnerDetails__attemptHeader">
                <strong>Attempt #{{ attempt.generation }}</strong>
                <v-chip small label :color="runnerAttemptColor(attempt.outcome)" text-color="white">
                  {{ runnerAttemptLabel(attempt.outcome) }}
                </v-chip>
              </div>
              <div>
                <div><strong>Runner:</strong> {{ runnerAttemptIdentity(attempt) }}</div>
                <div><strong>Executor:</strong> {{ runnerAttemptExecutorLabel(attempt) }}</div>
                <div
                  v-if="attempt.executor_type === 'k8s'"
                  class="mt-1"
                  data-testid="task-runner-attempt-k8s"
                >
                  <div><strong>Kubernetes:</strong> {{ kubernetesLocation(attempt) }}</div>
                  <div v-if="kubernetesRuntimeIdentity(attempt)">
                    <strong>Runtime:</strong> {{ kubernetesRuntimeIdentity(attempt) }}
                  </div>
                  <div v-if="attempt.k8s_lifecycle">
                    <strong>Kubernetes lifecycle:</strong> {{ attempt.k8s_lifecycle }}
                  </div>
                  <div v-if="attempt.k8s_terminal_reason">
                    <strong>Kubernetes terminal reason:</strong>
                    {{ attempt.k8s_terminal_reason }}
                  </div>
                  <div v-if="kubernetesPolicyReference(attempt)">
                    <strong>Kubernetes policy:</strong>
                    <code>{{ kubernetesPolicyReference(attempt) }}</code>
                  </div>
                  <div v-if="kubernetesWorkloadPolicy(attempt)">
                    <strong>Workload policy:</strong> {{ kubernetesWorkloadPolicy(attempt) }}
                  </div>
                  <div v-if="kubernetesResourceIdentities(attempt)">
                    <strong>Managed objects:</strong> {{ kubernetesResourceIdentities(attempt) }}
                  </div>
                  <div v-if="attempt.k8s_retention_state">
                    <strong>Retention:</strong> {{ kubernetesRetention(attempt) }}
                  </div>
                  <div
                    v-if="attempt.k8s_denial_rule_id"
                    class="error--text"
                    data-testid="task-runner-attempt-k8s-denial"
                  >
                    <strong>Policy denial:</strong> <code>{{ attempt.k8s_denial_rule_id }}</code>
                  </div>
                </div>
                <div v-if="attempt.container_name" class="mt-1">
                  <strong>Container:</strong>
                  <code>{{ attempt.container_name }}</code>
                </div>
                <div v-if="attempt.container_id" class="mt-1">
                  <strong>Container ID:</strong>
                  <code>{{ attempt.container_id }}</code>
                </div>
                <div class="mt-1">
                  <strong>Lifecycle:</strong> {{ runnerAttemptLabel(attempt.outcome) }}
                </div>
                <div class="text--secondary">
                  Assigned {{ attempt.assigned_at | formatDate }}
                  <span v-if="attempt.ended_at"> · Ended {{ attempt.ended_at | formatDate }}</span>
                  <span v-else> · In progress</span>
                </div>
                <div v-if="attempt.reason" class="mt-1" data-testid="task-runner-attempt-reason">
                  <strong>Terminal reason:</strong> {{ attempt.reason }}
                </div>
                <div v-if="attempt.requested_tags?.length" class="mt-1">
                  <strong>Tag policy:</strong>
                  {{ attempt.match_mode || 'all' }} · {{ attempt.requested_tags.join(', ') }}
                </div>
                <div v-if="attempt.placement_reason" class="mt-1 text--secondary">
                  {{ attempt.placement_reason }}
                </div>
                <div v-if="attempt.resolved_executor_image" class="mt-1">
                  <strong>Executor image:</strong>
                  <code>{{ attempt.resolved_executor_image }}</code>
                </div>
                <div v-if="attempt.docker_requested_image" class="mt-1">
                  <strong>Requested Docker image:</strong>
                  <code>{{ attempt.docker_requested_image }}</code>
                </div>
                <div v-if="attempt.docker_resolved_image" class="mt-1">
                  <strong>Resolved Docker digest:</strong>
                  <code>{{ attempt.docker_resolved_image }}</code>
                </div>
                <div v-if="dockerPolicyReference(attempt)" class="mt-1">
                  <strong>Docker policy:</strong>
                  <code>{{ dockerPolicyReference(attempt) }}</code>
                </div>
                <div v-if="dockerResourceLimits(attempt)" class="mt-1">
                  <strong>Docker limits:</strong> {{ dockerResourceLimits(attempt) }}
                </div>
                <div
                  v-if="attempt.denial_rule_id"
                  class="mt-1 error--text"
                  data-testid="task-runner-attempt-denial"
                >
                  <strong>Policy denial:</strong> <code>{{ attempt.denial_rule_id }}</code>
                </div>
              </div>
            </div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-row v-if="placementDecision?.evaluations?.length">
      <v-col cols="12">
        <v-card
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
          class="mb-5"
        >
          <v-card-title>Placement criteria</v-card-title>
          <v-card-text>
            <div
              v-for="evaluation in placementDecision.evaluations"
              :key="`${evaluation.scope}-${evaluation.runner_id}`"
              class="TaskRunnerDetails__placementEvaluation"
              data-testid="task-placement-evaluation"
            >
              <div>
                <strong>#{{ evaluation.runner_id }} — {{ evaluation.runner_name }}</strong>
                <span class="text--secondary ml-1">({{ evaluation.scope }})</span>
              </div>
              <div class="TaskRunnerDetails__criteria">
                <v-chip
                  v-for="criterion in evaluation.accepted_criteria"
                  :key="`accepted-${criterion}`"
                  x-small
                  outlined
                  color="success"
                >
                  {{ criterion }}
                </v-chip>
                <v-chip
                  v-for="criterion in evaluation.rejected_criteria"
                  :key="`rejected-${criterion}`"
                  x-small
                  outlined
                  color="error"
                >
                  {{ criterion }}
                </v-chip>
              </div>
            </div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>
  </div>
</template>

<script>
import axios from 'axios';

export default {
  props: {
    item: Object,
    projectId: Number,
  },
  data() {
    return {
      runnerAttempts: [],
      runnerAttemptsError: null,
      recoveryDiagnostics: null,
      recoveryDiagnosticsError: null,
      recoveryActionError: null,
      retryingRecovery: false,
      credentialUsage: [],
      credentialUsageError: null,
      loadedTaskId: null,
      loadedTaskStatus: null,
      loadedAssignmentGeneration: null,
      loadRevision: 0,
    };
  },
  computed: {
    placementDecision() {
      return this.item?.placement_decision || null;
    },
    placementRejected() {
      return this.placementDecision?.selected_runner_id == null;
    },
    isPro() {
      return (process.env.VUE_APP_BUILD_TYPE || '').startsWith('pro_');
    },
  },
  watch: {
    item: {
      deep: true,
      async handler(item) {
        if (item?.id !== this.loadedTaskId
            || item?.status !== this.loadedTaskStatus
            || item?.assignment_generation !== this.loadedAssignmentGeneration) {
          await this.loadRunnerAttempts();
        }
      },
    },
  },
  created() {
    this.loadRunnerAttempts();
  },
  methods: {
    runnerAttemptIdentity(attempt) {
      if (attempt.runner_id == null) return attempt.runner_name || '—';
      return attempt.runner_name
        ? `#${attempt.runner_id} — ${attempt.runner_name}`
        : `#${attempt.runner_id}`;
    },
    runnerAttemptExecutorLabel(attempt) {
      return attempt.executor_type || 'local';
    },
    kubernetesLocation(attempt) {
      return [attempt?.k8s_cluster_alias, attempt?.k8s_namespace]
        .filter(Boolean)
        .join(' · ');
    },
    kubernetesRuntimeIdentity(attempt) {
      const identities = [];
      if (attempt?.k8s_job_name) identities.push(`Job ${attempt.k8s_job_name}`);
      if (attempt?.k8s_pod_name) identities.push(`Pod ${attempt.k8s_pod_name}`);
      return identities.join(' · ');
    },
    kubernetesPolicyReference(attempt) {
      if (!attempt?.k8s_policy_revision && !attempt?.k8s_policy_hash) return '';
      const revision = attempt.k8s_policy_revision
        ? `revision ${attempt.k8s_policy_revision}`
        : 'revision not reported';
      return attempt.k8s_policy_hash
        ? `${revision} · ${attempt.k8s_policy_hash}`
        : revision;
    },
    kubernetesWorkloadPolicy(attempt) {
      const values = [];
      if (attempt?.k8s_service_account) values.push(`service account ${attempt.k8s_service_account}`);
      if (attempt?.k8s_runtime_class) values.push(`runtime ${attempt.k8s_runtime_class}`);
      if (attempt?.k8s_network_profile) {
        const enforcement = attempt.k8s_network_enforcement
          ? ` (${attempt.k8s_network_enforcement})`
          : '';
        values.push(`network ${attempt.k8s_network_profile}${enforcement}`);
      }
      if (attempt?.k8s_resource_policy_id) {
        values.push(`resources ${attempt.k8s_resource_policy_id}`);
      }
      return values.join(' · ');
    },
    kubernetesResourceIdentities(attempt) {
      const values = [];
      if (attempt?.k8s_secret_name && attempt?.k8s_secret_uid) {
        values.push(`Bundle ${attempt.k8s_secret_name} (${attempt.k8s_secret_uid})`);
      }
      if (attempt?.k8s_network_policy_name && attempt?.k8s_network_policy_uid) {
        values.push(`NetworkPolicy ${attempt.k8s_network_policy_name} (${attempt.k8s_network_policy_uid})`);
      }
      return values.join(' · ');
    },
    kubernetesRetention(attempt) {
      if (!attempt?.k8s_retention_deadline) return attempt?.k8s_retention_state || '';
      return `${attempt.k8s_retention_state} · until ${attempt.k8s_retention_deadline}`;
    },
    dockerPolicyReference(attempt) {
      if (!attempt?.docker_policy_revision && !attempt?.docker_policy_hash) return '';
      const revision = attempt.docker_policy_revision
        ? `revision ${attempt.docker_policy_revision}`
        : 'revision not reported';
      return attempt.docker_policy_hash
        ? `${revision} · ${attempt.docker_policy_hash}`
        : revision;
    },
    dockerResourceLimits(attempt) {
      const limits = [];
      if (attempt?.docker_nano_cpus) {
        limits.push(`${Number(attempt.docker_nano_cpus) / 1_000_000_000} CPU`);
      }
      if (attempt?.docker_memory_bytes) {
        limits.push(`${Math.round(Number(attempt.docker_memory_bytes) / 1024 / 1024)} MiB`);
      }
      if (attempt?.docker_pids_limit) {
        limits.push(`${attempt.docker_pids_limit} PIDs`);
      }
      return limits.join(' · ');
    },
    runnerAttemptLabel(outcome) {
      return {
        active: 'Active',
        requeued: 'Requeued',
        succeeded: 'Succeeded',
        failed: 'Failed',
        stopped: 'Stopped',
      }[outcome] || outcome;
    },
    runnerAttemptColor(outcome) {
      return {
        active: 'info',
        requeued: 'warning',
        succeeded: 'success',
        failed: 'error',
        stopped: 'grey darken-1',
      }[outcome] || 'grey';
    },
    recoveryDecisionColor(decision) {
      return {
        observe: 'info',
        recover: 'success',
        quarantine: 'warning',
      }[decision] || 'grey';
    },
    async loadRunnerAttempts() {
      const taskId = this.item?.id;
      const revision = this.loadRevision + 1;
      this.loadRevision = revision;
      if (taskId == null) return;
      try {
        const { data } = await axios.get(
          `/api/project/${this.projectId}/tasks/${taskId}/runner-attempts`,
        );
        if (this.item?.id !== taskId || this.loadRevision !== revision) return;
        this.runnerAttempts = data || [];
        this.runnerAttemptsError = null;
      } catch {
        if (this.item?.id !== taskId || this.loadRevision !== revision) return;
        this.runnerAttempts = [];
        this.runnerAttemptsError = 'Runner attempt history could not be loaded.';
      }
      await this.loadTaskRecoveryDiagnostics(taskId, revision);
      if (this.isPro) await this.loadCredentialUsage(taskId, revision);
      this.loadedTaskId = taskId;
      this.loadedTaskStatus = this.item?.status;
      this.loadedAssignmentGeneration = this.item?.assignment_generation;
    },
    async loadTaskRecoveryDiagnostics(taskId, revision) {
      try {
        const { data } = await axios.get(
          `/api/project/${this.projectId}/tasks/${taskId}/recovery`,
        );
        if (this.item?.id !== taskId || this.loadRevision !== revision) return;
        this.recoveryDiagnostics = data?.controlled ? data : null;
        this.recoveryDiagnosticsError = null;
      } catch {
        if (this.item?.id !== taskId || this.loadRevision !== revision) return;
        this.recoveryDiagnostics = null;
        this.recoveryDiagnosticsError = 'HA task recovery diagnostics could not be loaded.';
      }
    },
    async loadCredentialUsage(taskId, revision) {
      try {
        const { data } = await axios.get(
          `/api/project/${this.projectId}/tasks/${taskId}/credential-usage?count=100`,
        );
        if (this.item?.id !== taskId || this.loadRevision !== revision) return;
        this.credentialUsage = data || [];
        this.credentialUsageError = null;
      } catch {
        if (this.item?.id !== taskId || this.loadRevision !== revision) return;
        this.credentialUsage = [];
        this.credentialUsageError = 'Credential provenance could not be loaded.';
      }
    },
    shortCredentialFingerprint(value) {
      return value ? `${value.slice(0, 12)}…` : 'not recorded';
    },
    credentialOutcomeColor(outcome) {
      return { allowed: 'success', denied: 'warning', failure: 'error' }[outcome] || 'grey';
    },
    async retryTaskRecovery() {
      if (this.item?.id == null || this.retryingRecovery) return;
      this.retryingRecovery = true;
      this.recoveryActionError = null;
      try {
        await axios.post(
          `/api/project/${this.projectId}/tasks/${this.item.id}/retry-recovery`,
        );
        await this.loadRunnerAttempts();
      } catch (error) {
        this.recoveryActionError = error?.response?.data?.error
          || 'The safe recovery check could not be retried.';
      } finally {
        this.retryingRecovery = false;
      }
    },
  },
};
</script>

<style scoped>
.TaskRunnerDetails__attempt {
  display: grid;
  grid-template-columns: minmax(170px, 0.35fr) minmax(0, 1fr);
  gap: 16px;
  padding: 14px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.TaskRunnerDetails__attempt:first-child,
.TaskRunnerDetails__placementEvaluation:first-child {
  border-top: 0;
  padding-top: 0;
}

.TaskRunnerDetails__attemptHeader {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.TaskRunnerDetails__attempt code {
  overflow-wrap: anywhere;
}

.TaskRunnerDetails__placementEvaluation {
  padding: 12px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.TaskRunnerDetails__credentialUsage {
  padding: 10px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
  overflow-wrap: anywhere;
}

.TaskRunnerDetails__credentialUsage:first-child {
  border-top: 0;
  padding-top: 0;
}

.TaskRunnerDetails__criteria {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.TaskRunnerDetails__recoveryHeader {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.TaskRunnerDetails__recoveryGrid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px 16px;

  code {
    overflow-wrap: anywhere;
  }
}

@media (max-width: 600px) {
  .TaskRunnerDetails__attempt {
    grid-template-columns: 1fr;
    gap: 8px;
  }

  .TaskRunnerDetails__recoveryGrid {
    grid-template-columns: 1fr;
  }
}
</style>

<style>
.TaskRunnerDetails__recoveryAlert .v-alert__content {
  min-width: 0;
}
</style>
