<template xmlns:v-slot="http://www.w3.org/1999/XSL/Transform">
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

    <v-row>
      <v-col cols="12" md="6">
        <v-card
          v-if="template"
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
        >
          <v-card-title>Template info</v-card-title>
          <v-card-text>
            <v-simple-table class="TaskDetails__table">
              <template v-slot:default>
                <tbody>
                <tr>
                  <td><b>App</b></td>
                  <td>{{ getAppTitle(template.app) }}</td>
                </tr>
                <tr>
                  <td><b>Template</b></td>
                  <td>
                    <RouterLink :to="`/project/${projectId}/templates/${template.id}`">
                      {{ template.name }}
                    </RouterLink>
                  </td>
                </tr>
                </tbody>
              </template>
            </v-simple-table>
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="6">
        <v-card
          v-if="item.commit_hash"
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
        >
          <v-card-title>Commit info</v-card-title>

          <v-card-text>
            <v-simple-table class="TaskDetails__table">
              <template v-slot:default>
                <tbody>
                <tr>
                  <td><b>Message</b></td>
                  <td>{{ item.commit_message }}</td>
                </tr>
                <tr>
                  <td><b>Hash</b></td>
                  <td>{{ item.commit_hash }}</td>
                </tr>
                </tbody>
              </template>
            </v-simple-table>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-row>
      <v-col cols="12" md="6">
        <v-card
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
          class="mb-5"
        >
          <v-card-title>Running info</v-card-title>
          <v-card-text>
            <v-simple-table class="pa-0 TaskDetails__table">
              <template v-slot:default>
                <tbody>
                <tr>
                  <td><b>Message</b></td>
                  <td>{{ item.message || '—' }}</td>
                </tr>
                <tr v-if="item.user_id != null">
                  <td><b>{{ $t('author') }}</b></td>
                  <td>{{ user?.name || '—' }}</td>
                </tr>
                <tr v-else-if="item.integration_id != null">
                  <td><b>{{ $t('integration') }}</b></td>
                  <td>
                    <router-link
                      v-if="isReady(integration)"
                      :to="`/project/${projectId}/integrations/${item.integration_id}`"
                    >{{ integration.data.name }}</router-link>
                    <span v-else>{{ originLabel(integration, item.integration_id) }}</span>
                  </td>
                </tr>
                <tr v-else-if="item.schedule_id != null">
                  <td><b>{{ $t('schedule') }}</b></td>
                  <td>
                    <!-- Schedules have no detail page, so link to the list. -->
                    <router-link
                      v-if="isReady(schedule)"
                      :to="`/project/${projectId}/schedule`"
                    >{{ schedule.data.name || $t('unnamedSchedule') }}</router-link>
                    <span v-else>{{ originLabel(schedule, item.schedule_id) }}</span>
                  </td>
                </tr>
                <tr>
                  <td><b>{{ $t('created') }}</b></td>
                  <td>{{ item.created | formatDate }}</td>
                </tr>
                <tr>
                  <td><b>{{ $t('started') }}</b></td>
                  <td>{{ item.start | formatDate }}</td>
                </tr>
                <tr>
                  <td><b>{{ $t('end') }}</b></td>
                  <td>{{ item.end | formatDate }}</td>
                </tr>
                <tr>
                  <td><b>{{ $t('duration') }}</b></td>
                  <td>{{ [item.start, item.end] | formatMilliseconds }}</td>
                </tr>
                <tr v-if="runnerIdentity">
                  <td><b>Runner</b></td>
                  <td data-testid="task-runner-identity">{{ runnerIdentity }}</td>
                </tr>
                <tr v-if="item.requested_executor_image">
                  <td><b>Requested executor image</b></td>
                  <td data-testid="task-requested-executor-image">
                    <code>{{ item.requested_executor_image }}</code>
                  </td>
                </tr>
                <tr v-if="item.resolved_executor_image">
                  <td><b>Resolved executor image</b></td>
                  <td data-testid="task-resolved-executor-image">
                    <code>{{ item.resolved_executor_image }}</code>
                  </td>
                </tr>
                </tbody>
              </template>
            </v-simple-table>
          </v-card-text>
        </v-card>
      </v-col>
      <v-col cols="12" md="6">
        <v-card
          v-if="item?.params"
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
          class="mb-5"
        >
          <v-card-title>Task parameters</v-card-title>
          <v-card-text>
            <v-simple-table class="pa-0 TaskDetails__table">
              <template v-slot:default>
                <tbody>
                <tr>
                  <td><b>Branch</b></td>
                  <td>
                    {{ item.get_branch || '—' }}
                  </td>
                </tr>
                <tr>
                  <td><b>Limit</b></td>
                  <td>
                    <span v-if="Array.isArray(item.params.limit) && item.params.limit.length > 0">
                      {{ item.params.limit.join(', ') }}</span>
                    <span v-else>'No'</span>
                  </td>
                </tr>
                <tr>
                  <td><b>Debug</b></td>
                  <td>
                    {{ item.params.debug ? 'Yes' : 'No' }}
                  </td>
                </tr>
                <tr>
                  <td><b>Debug level</b></td>
                  <td>{{ item.params.debug_level || '—' }}</td>
                </tr>
                <tr>
                  <td><b>Diff</b> <code>--diff</code></td>
                  <td>{{ item.params.diff ? 'Yes' : 'No' }}</td>
                </tr>
                <tr>
                  <td><b>Dry run</b> <code>--check</code></td>
                  <td>{{ item.params.dry_run ? 'Yes' : 'No' }}</td>
                </tr>
                <tr>
                  <td><b>Environment</b></td>
                  <td>
                    {{ !item.environment || item.environment === '{}' ? '—' : item.environment }}
                  </td>
                </tr>
                </tbody>
              </template>
            </v-simple-table>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-row v-if="parsedArtifacts">
      <v-col cols="12">
        <v-card
          :color="$vuetify.theme.dark ? '#212121' : 'white'"
          style="background: #8585850f"
          class="mb-5"
        >
          <v-card-title>
            {{ $t('workflowArtifacts') }}
            <v-tooltip bottom max-width="320">
              <template v-slot:activator="{ on, attrs }">
                <v-icon small class="ml-2" v-bind="attrs" v-on="on">
                  mdi-information-outline
                </v-icon>
              </template>
              <span>{{ $t('workflowArtifactsHint') }}</span>
            </v-tooltip>
          </v-card-title>
          <v-card-text>
            <pre class="TaskDetails__artifacts">{{ formattedArtifacts }}</pre>
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
              class="TaskDetails__attempt"
              data-testid="task-runner-attempt"
            >
              <div class="TaskDetails__attemptHeader">
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
              class="TaskDetails__placementEvaluation"
              data-testid="task-placement-evaluation"
            >
              <div>
                <strong>#{{ evaluation.runner_id }} — {{ evaluation.runner_name }}</strong>
                <span class="text--secondary ml-1">({{ evaluation.scope }})</span>
              </div>
              <div class="TaskDetails__criteria">
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

<style lang="scss">
.TaskDetails__table {
  background-color: transparent !important;

  table {
    width: 100%;
    table-layout: fixed;
  }

  td {
    white-space: normal;
    overflow-wrap: anywhere;
  }

  td:first-child {
    width: 42%;
  }

  code {
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .v-data-table__wrapper {
    padding-left: 0 !important;
    padding-right: 0 !important;
  }
}

.TaskDetails__artifacts {
  white-space: pre-wrap;
  word-break: break-word;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  margin: 0;
}

.TaskDetails__attempt {
  display: grid;
  grid-template-columns: minmax(170px, 0.35fr) minmax(0, 1fr);
  gap: 16px;
  padding: 14px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.TaskDetails__attempt:first-child {
  border-top: 0;
  padding-top: 0;
}

.TaskDetails__attemptHeader {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 8px;
}

.TaskDetails__placementEvaluation {
  padding: 12px 0;
  border-top: 1px solid rgba(128, 128, 128, 0.25);
}

.TaskDetails__placementEvaluation:first-child {
  padding-top: 0;
  border-top: 0;
}

.TaskDetails__criteria {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

@media (max-width: 600px) {
  .TaskDetails__table {
    table,
    tbody,
    tr,
    td {
      display: block;
      width: 100% !important;
    }

    tr {
      padding: 8px 0;
      border-bottom: thin solid rgba(0, 0, 0, 0.12);
    }

    td {
      height: auto !important;
      padding-top: 3px !important;
      padding-bottom: 3px !important;
      border-bottom: 0 !important;
    }
  }

  .TaskDetails__attempt {
    grid-template-columns: 1fr;
    gap: 8px;
  }
}

</style>

<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/task-details';

import ProjectMixin from '@/components/ProjectMixin';
import AppsMixin from '@/components/AppsMixin';

export default {
  props: {
    item: Object,
    user: Object,
    // { status: 'loading' | 'ready' | 'missing' | 'error', data } for the
    // schedule or integration that started the task; null when a person did.
    schedule: Object,
    integration: Object,
    projectId: Number,
  },

  mixins: [ProjectMixin, AppsMixin],

  data() {
    return {
      template: null,
      runnerAttempts: [],
      runnerAttemptsError: null,
      loadedTaskId: null,
      loadedTaskStatus: null,
      loadedAssignmentGeneration: null,
      loadRevision: 0,
    };
  },

  watch: {
    item: {
      deep: true,
      async handler(item) {
        if (item?.id !== this.loadedTaskId
            || item?.template_id !== this.template?.id
            || item?.status !== this.loadedTaskStatus
            || item?.assignment_generation !== this.loadedAssignmentGeneration) {
          await this.loadData();
        }
      },
    },
  },

  computed: {
    ...enhancedComputed,

    parsedArtifacts() {
      const raw = this.item?.artifacts;
      if (raw == null || raw === '') return null;
      if (typeof raw === 'object') {
        return Object.keys(raw).length === 0 ? null : raw;
      }
      try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)
            && Object.keys(parsed).length > 0) {
          return parsed;
        }
        return null;
      } catch (e) {
        return null;
      }
    },
    formattedArtifacts() {
      return this.parsedArtifacts ? JSON.stringify(this.parsedArtifacts, null, 2) : '';
    },
  },

  async created() {
    await this.loadData();
  },

  methods: {
    ...enhancedMethods,
    isReady(origin) {
      return origin != null && origin.status === 'ready';
    },

    // Only a confirmed 404 says "deleted"; while loading or after an unrelated
    // failure the id alone is all we can honestly show.
    originLabel(origin, id) {
      if (origin != null && origin.status === 'missing') {
        return this.$t('deletedOrigin', { id });
      }
      return `#${id}`;
    },

    async loadData() {
      const taskId = this.item?.id;
      const templateId = this.item?.template_id;
      const revision = this.loadRevision + 1;
      this.loadRevision = revision;
      const [template, runnerAttemptResult] = await Promise.all([
        templateId == null ? null : this.loadProjectResource('templates', templateId),
        this.loadRunnerAttempts(taskId),
      ]);
      if (this.item?.id !== taskId || this.loadRevision !== revision) return;
      this.template = template;
      this.runnerAttempts = runnerAttemptResult.attempts;
      this.runnerAttemptsError = runnerAttemptResult.error;
      this.loadedTaskId = taskId;
      this.loadedTaskStatus = this.item?.status;
      this.loadedAssignmentGeneration = this.item?.assignment_generation;
    },
  },
};
</script>
