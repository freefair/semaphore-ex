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
                <div class="text--secondary">
                  Assigned {{ attempt.assigned_at | formatDate }}
                  <span v-if="attempt.ended_at"> · Ended {{ attempt.ended_at | formatDate }}</span>
                  <span v-else> · In progress</span>
                </div>
                <div v-if="attempt.reason" class="mt-1" data-testid="task-runner-attempt-reason">
                  {{ attempt.reason }}
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
      this.loadedTaskId = taskId;
      this.loadedTaskStatus = this.item?.status;
      this.loadedAssignmentGeneration = this.item?.assignment_generation;
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

.TaskRunnerDetails__placementEvaluation {
  padding: 12px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.TaskRunnerDetails__criteria {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

@media (max-width: 600px) {
  .TaskRunnerDetails__attempt {
    grid-template-columns: 1fr;
    gap: 8px;
  }
}
</style>
