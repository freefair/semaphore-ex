<template>
  <section data-testid="policy-guardrails-panel">
    <h2 class="mt-8 mb-1">{{ $t('policyGuardrails') }}</h2>
    <v-divider class="mb-5" />

    <v-alert v-if="error" variant="tonal" type="error" dismissible @update:model-value="error = ''">
      {{ error }}
    </v-alert>
    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-4" />

    <template v-if="canManage && state">
      <div class="d-flex flex-wrap align-center mb-3 policy-guardrail-status">
        <v-chip size="small" variant="outlined" class="mr-2 mb-2">
          {{ $t('policyGuardrailDraftRevision') }} {{ state.draft.revision }}
        </v-chip>
        <v-chip v-if="state.active" size="small" color="success" dark class="mr-2 mb-2">
          {{ $t('policyGuardrailActiveRevision') }} {{ state.active.revision }}
        </v-chip>
        <v-chip v-else size="small" variant="outlined" class="mr-2 mb-2">
          {{ $t('policyGuardrailNoActiveRevision') }}
        </v-chip>
      </div>

      <v-textarea
        v-model="sourceYaml"
        :label="$t('policyGuardrailYaml')"
        variant="outlined"
        rows="9"
        :disabled="busy"
        data-testid="policy-guardrail-source"
      />

      <div class="d-flex flex-wrap mb-4 policy-guardrail-actions">
        <v-btn
          size="small"
          color="primary"
          class="mr-2 mb-2"
          :disabled="!canWrite || busy"
          :loading="action === 'save'"
          data-testid="policy-guardrail-save"
          @click="saveDraft"
        >
          {{ $t('saveDraft') }}
        </v-btn>
        <v-btn
          size="small"
          variant="outlined"
          color="primary"
          class="mr-2 mb-2"
          :disabled="!canWrite || busy"
          :loading="action === 'validate'"
          data-testid="policy-guardrail-validate"
          @click="validateDraft"
        >
          {{ $t('validate') }}
        </v-btn>
        <v-btn
          size="small"
          variant="outlined"
          color="success"
          class="mr-2 mb-2"
          :disabled="!canWrite || busy"
          :loading="action === 'publish'"
          data-testid="policy-guardrail-publish"
          @click="publishDraft"
        >
          {{ $t('publish') }}
        </v-btn>
        <v-btn size="small" variant="text" :disabled="busy" @click="load">
          <v-icon start size="small">mdi-refresh</v-icon>
          {{ $t('refresh') }}
        </v-btn>
      </div>

      <v-alert
        v-if="validation"
        :type="validation.valid ? 'success' : 'warning'"
        variant="tonal"
        density="compact"
        data-testid="policy-guardrail-validation"
      >
        <span v-if="validation.valid">
          {{ $t('policyGuardrailValid', { count: validation.rule_count }) }}
        </span>
        <span v-else>{{ validationIssue }}</span>
      </v-alert>

      <v-expansion-panels variant="accordion" flat class="mb-4">
        <v-expansion-panel>
          <v-expansion-panel-title>
            {{ $t('policyGuardrailFixtureAndImpact') }}
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <p class="text-body-medium grey--text text--darken-1">
              {{ $t('policyGuardrailFixtureHint') }}
            </p>
            <v-textarea
              v-model="fixtureJson"
              :label="$t('policyGuardrailFixtureInput')"
              variant="outlined"
              rows="9"
              :disabled="busy"
              data-testid="policy-guardrail-fixture"
            />
            <div class="d-flex flex-wrap">
              <v-btn
                size="small"
                variant="outlined"
                color="primary"
                class="mr-2 mb-2"
                :disabled="!canExecute || busy"
                :loading="action === 'fixture'"
                data-testid="policy-guardrail-test"
                @click="testFixture"
              >
                {{ $t('test') }}
              </v-btn>
              <v-btn
                size="small"
                variant="outlined"
                color="primary"
                class="mb-2"
                :disabled="!canExecute || busy"
                :loading="action === 'impact'"
                data-testid="policy-guardrail-impact"
                @click="previewImpact"
              >
                {{ $t('policyGuardrailImpactPreview') }}
              </v-btn>
            </div>
            <pre
              v-if="fixtureResult"
              class="policy-guardrail-result"
              data-testid="policy-guardrail-fixture-result"
            >{{ pretty(fixtureResult) }}</pre>
            <pre
              v-if="impactResult"
              class="policy-guardrail-result"
              data-testid="policy-guardrail-impact-result"
            >{{ pretty(impactResult) }}</pre>
          </v-expansion-panel-text>
        </v-expansion-panel>

        <v-expansion-panel>
          <v-expansion-panel-title>
            {{ $t('policyGuardrailHistoryAndDiff') }}
          </v-expansion-panel-title>
          <v-expansion-panel-text>
            <v-row density="compact">
              <v-col cols="12" sm="5">
                <v-select
                  v-model="diffFrom"
                  :items="revisionOptions"
                  item-title="text"
                  :label="$t('policyGuardrailFromRevision')"
                  variant="outlined"
                  density="compact"
                  :disabled="busy"
                />
              </v-col>
              <v-col cols="12" sm="5">
                <v-select
                  v-model="diffTo"
                  :items="revisionOptions"
                  item-title="text"
                  :label="$t('policyGuardrailToRevision')"
                  variant="outlined"
                  density="compact"
                  :disabled="busy"
                />
              </v-col>
              <v-col cols="12" sm="2" class="d-flex align-start">
                <v-btn
                  size="small"
                  variant="outlined"
                  color="primary"
                  :disabled="!canReadDiff || busy"
                  :loading="action === 'diff'"
                  data-testid="policy-guardrail-diff"
                  @click="loadDiff"
                >
                  {{ $t('diff') }}
                </v-btn>
              </v-col>
            </v-row>
            <pre
              v-if="diffResult"
              class="policy-guardrail-result"
              data-testid="policy-guardrail-diff-result"
            >{{ pretty(diffResult) }}</pre>

            <v-data-table
              :headers="revisionHeaders"
              :items="revisions"
              density="compact"
              :hide-default-footer="true"
              item-value="id"
              data-testid="policy-guardrail-revisions"
            >
              <template v-slot:item.created="{ item }">
                {{ formatDate(item.created) }}
              </template>
              <template v-slot:item.rollback="{ item }">
                {{ item.rollback_of_revision || '—' }}
              </template>
              <template v-slot:no-data>
                <div class="py-3 grey--text">{{ $t('policyGuardrailNoRevisions') }}</div>
              </template>
            </v-data-table>

            <v-data-table
              class="mt-4"
              :headers="evaluationHeaders"
              :items="evaluations"
              density="compact"
              :hide-default-footer="true"
              item-value="id"
              data-testid="policy-guardrail-evaluations"
            >
              <template v-slot:item.decision="{ item }">
                <v-chip
                  size="x-small"
                  dark
                  :color="item.decision === 'deny' ? 'error' : 'success'"
                >
                  {{ item.decision }}
                </v-chip>
              </template>
              <template v-slot:item.created="{ item }">
                {{ formatDate(item.created) }}
              </template>
              <template v-slot:no-data>
                <div class="py-3 grey--text">{{ $t('policyGuardrailNoEvaluations') }}</div>
              </template>
            </v-data-table>
          </v-expansion-panel-text>
        </v-expansion-panel>
      </v-expansion-panels>
    </template>

    <v-card
      v-if="canRollbackAction && (revisionOptions.length || !canManage)"
      variant="outlined"
      data-testid="policy-guardrail-rollback-card"
    >
      <v-card-title class="subtitle-1">{{ $t('policyGuardrailRollback') }}</v-card-title>
      <v-card-text>
        <v-select
          v-if="revisionOptions.length"
          v-model="rollbackRevision"
          :items="revisionOptions"
          item-title="text"
          :label="$t('policyGuardrailTargetRevision')"
          variant="outlined"
          density="compact"
          :disabled="busy"
        />
        <v-text-field
          v-else
          v-model.number="rollbackRevision"
          type="number"
          min="1"
          :label="$t('policyGuardrailTargetRevision')"
          variant="outlined"
          density="compact"
          :disabled="busy"
        />
        <v-text-field
          v-if="!state"
          v-model.number="rollbackExpectedRevision"
          type="number"
          min="1"
          :label="$t('policyGuardrailDraftRevision')"
          variant="outlined"
          density="compact"
          :disabled="busy"
        />
        <v-textarea
          v-model.trim="rollbackReason"
          :label="$t('policyGuardrailRollbackReason')"
          variant="outlined"
          rows="2"
          counter="512"
          :disabled="busy"
          data-testid="policy-guardrail-rollback-reason"
        />
      </v-card-text>
      <v-card-actions class="px-4 pb-4">
        <v-btn
          color="warning"
          :disabled="!rollbackReady || busy"
          :loading="action === 'rollback'"
          data-testid="policy-guardrail-rollback"
          @click="rollback"
        >
          {{ $t('policyGuardrailRollback') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </section>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

const PAGE_SIZE = 25;

export default {
  name: 'PolicyGuardrailsPanel',

  props: {
    projectId: { type: Number, default: null },
    capability: { type: Object, required: true },
    canManage: { type: Boolean, default: false },
    canRollback: { type: Boolean, default: false },
  },

  data() {
    return {
      state: null,
      sourceYaml: '',
      revisions: [],
      evaluations: [],
      validation: null,
      fixtureJson: this.defaultFixtureJson(),
      fixtureResult: null,
      impactResult: null,
      diffFrom: null,
      diffTo: null,
      diffResult: null,
      rollbackRevision: null,
      rollbackExpectedRevision: null,
      rollbackReason: '',
      loading: false,
      action: '',
      error: '',
    };
  },

  computed: {
    baseUrl() {
      return this.projectId
        ? `/api/project/${this.projectId}/policy-guardrails`
        : '/api/policy-guardrails';
    },
    busy() {
      return this.loading || this.action !== '';
    },
    canWrite() {
      return this.canManage && Boolean(this.capability.access?.includes('write'));
    },
    canExecute() {
      return this.canManage && Boolean(this.capability.access?.includes('execute'));
    },
    canRollbackAction() {
      return this.canRollback && Boolean(this.capability.access?.includes('write'));
    },
    canReadDiff() {
      return this.canManage && this.diffFrom > 0 && this.diffTo > 0
        && this.diffFrom !== this.diffTo;
    },
    rollbackReady() {
      return this.canRollbackAction && this.rollbackRevision > 0
        && this.expectedRollbackRevision > 0
        && this.rollbackReason.length > 0 && this.rollbackReason.length <= 512;
    },
    expectedRollbackRevision() {
      return this.state?.draft?.revision || this.rollbackExpectedRevision;
    },
    revisionOptions() {
      return this.revisions.map(({ revision }) => ({ text: `#${revision}`, value: revision }));
    },
    validationIssue() {
      return this.validation?.issues?.[0]?.message || this.$t('policyGuardrailInvalid');
    },
    revisionHeaders() {
      return [
        { title: this.$t('revision'), key: 'revision', sortable: false },
        { title: this.$t('policyGuardrailRollbackOf'), key: 'rollback', sortable: false },
        { title: this.$t('created'), key: 'created', sortable: false },
      ];
    },
    evaluationHeaders() {
      return [
        { title: this.$t('decision'), key: 'decision', sortable: false },
        { title: this.$t('source'), key: 'source', sortable: false },
        { title: this.$t('created'), key: 'created', sortable: false },
      ];
    },
  },

  async created() {
    if (this.canManage && this.capability.access?.includes('read')) {
      await this.load();
    }
  },

  methods: {
    defaultFixtureJson() {
      return JSON.stringify({
        project_id: this.projectId || 1,
        intent: 'task',
        evaluated_at: new Date().toISOString(),
        template: { id: 1, application: 'ansible', source: 'manual' },
        environment_ids: [],
        runner: { requested_tags: [], candidate_count: 0 },
        executor: { type: 'unknown', image_present: false, image_reference_kind: 'none' },
        credentials: [],
      }, null, 2);
    },
    async load() {
      if (!this.canManage) return;
      this.loading = true;
      this.error = '';
      try {
        const [state, revisions, evaluations] = await Promise.all([
          axios.get(this.baseUrl),
          axios.get(`${this.baseUrl}/revisions`, { params: { count: PAGE_SIZE } }),
          axios.get(`${this.baseUrl}/evaluations`, { params: { count: PAGE_SIZE } }),
        ]);
        this.state = state.data;
        this.sourceYaml = this.state?.draft?.source_yaml || '';
        this.revisions = revisions.data || [];
        this.evaluations = evaluations.data || [];
        this.rollbackExpectedRevision = this.state?.draft?.revision || null;
        this.selectRevisionDefaults();
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.loading = false;
      }
    },
    selectRevisionDefaults() {
      if (!this.rollbackRevision && this.revisions.length) {
        this.rollbackRevision = this.revisions[0].revision;
      }
      if (!this.diffTo && this.revisions.length) {
        this.diffTo = this.revisions[0].revision;
      }
      if (!this.diffFrom && this.revisions.length > 1) {
        this.diffFrom = this.revisions[1].revision;
      }
    },
    async perform(name, callback) {
      this.action = name;
      this.error = '';
      try {
        return await callback();
      } catch (err) {
        this.error = getErrorMessage(err);
        return null;
      } finally {
        this.action = '';
      }
    },
    async saveDraft() {
      const response = await this.perform('save', () => axios.put(`${this.baseUrl}/draft`, {
        source_yaml: this.sourceYaml,
        expected_revision: this.state.draft.revision,
      }));
      if (!response) return;
      this.state = { ...this.state, draft: response.data };
      this.rollbackExpectedRevision = response.data.revision;
    },
    async validateDraft() {
      const response = await this.perform('validate', () => axios.post(`${this.baseUrl}/validate`, { source_yaml: this.sourceYaml }));
      if (response) this.validation = response.data;
    },
    parseFixture() {
      try {
        return JSON.parse(this.fixtureJson);
      } catch (err) {
        this.error = this.$t('policyGuardrailFixtureInvalidJson');
        return null;
      }
    },
    async testFixture() {
      const input = this.parseFixture();
      if (!input) return;
      const response = await this.perform('fixture', () => axios.post(`${this.baseUrl}/test`, { source_yaml: this.sourceYaml, input }));
      if (response) this.fixtureResult = response.data;
    },
    async previewImpact() {
      const input = this.parseFixture();
      if (!input) return;
      const response = await this.perform('impact', () => axios.post(`${this.baseUrl}/impact`, { inputs: [input] }));
      if (response) this.impactResult = response.data;
    },
    async publishDraft() {
      const response = await this.perform('publish', () => axios.post(`${this.baseUrl}/publish`, {
        expected_draft_revision: this.state.draft.revision,
      }));
      if (response) await this.load();
    },
    async loadDiff() {
      const response = await this.perform('diff', () => axios.get(`${this.baseUrl}/diff`, {
        params: { from_revision: this.diffFrom, to_revision: this.diffTo },
      }));
      if (response) this.diffResult = response.data;
    },
    async rollback() {
      const response = await this.perform('rollback', () => axios.post(`${this.baseUrl}/rollback`, {
        revision: Number(this.rollbackRevision),
        expected_draft_revision: Number(this.expectedRollbackRevision),
        reason: this.rollbackReason,
      }));
      if (!response) return;
      this.rollbackReason = '';
      await this.load();
    },
    pretty(value) {
      return JSON.stringify(value, null, 2);
    },
    formatDate(value) {
      if (!value) return '—';
      return new Date(value).toLocaleString();
    },
  },
};
</script>

<style scoped>
.policy-guardrail-actions {
  gap: 4px;
}

.policy-guardrail-result {
  max-height: 240px;
  overflow: auto;
  padding: 12px;
  margin-top: 12px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.12);
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
