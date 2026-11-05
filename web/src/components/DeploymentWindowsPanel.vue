<template>
  <section data-testid="deployment-windows-panel">
    <YesNoDialog
      v-model="resetDialog"
      :title="$t('deploymentWindowsReset')"
      :text="$t('deploymentWindowsResetConfirm')"
      @yes="resetPolicy"
    />

    <h2 class="mt-8 mb-1">{{ $t('deploymentWindows') }}</h2>
    <v-divider class="mb-5" />

    <v-alert v-if="error" variant="tonal" type="error" dismissible @update:model-value="error = ''">
      {{ error }}
    </v-alert>
    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-4" />

    <template v-if="policy">
      <v-row density="compact">
        <v-col cols="12" sm="7">
          <v-text-field
            v-model.trim="policy.timezone"
            :label="$t('deploymentWindowsTimezone')"
            hint="IANA: Europe/Berlin"
            persistent-hint
            variant="outlined"
            density="compact"
            :disabled="!canWrite || saving"
            data-testid="deployment-windows-timezone"
          />
        </v-col>
        <v-col cols="12" sm="5">
          <v-select
            v-model="policy.default"
            :items="defaultOptions"
            item-title="text"
            :label="$t('deploymentWindowsDefault')"
            variant="outlined"
            density="compact"
            :disabled="!canWrite || saving"
          />
        </v-col>
      </v-row>

      <div class="d-flex align-center mt-2 mb-3">
        <div class="subtitle-1">{{ $t('deploymentWindowRules') }}</div>
        <v-spacer />
        <v-btn
          v-if="canWrite"
          size="small"
          variant="outlined"
          color="primary"
          :disabled="saving || policy.rules.length >= 64"
          @click="addRule"
          data-testid="deployment-window-add-rule"
        >
          <v-icon start size="small">mdi-plus</v-icon>
          {{ $t('add') }}
        </v-btn>
      </div>

      <v-alert v-if="policy.rules.length === 0" variant="tonal" density="compact" type="info">
        {{ $t('deploymentWindowRulesEmpty') }}
      </v-alert>

      <v-card
        v-for="(rule, index) in policy.rules"
        :key="ruleKey(rule, index)"
        variant="outlined"
        class="pa-3 mb-3"
        data-testid="deployment-window-rule"
      >
        <div class="d-flex align-center mb-2">
          <v-switch
            v-model="rule.active"
            :label="$t('active')"
            density="compact"
            hide-details
            class="mt-0"
            :disabled="!canWrite || saving"
          />
          <v-spacer />
          <v-btn
            v-if="canWrite"
            icon
            size="small"
            :aria-label="$t('delete')"
            :disabled="saving"
            @click="removeRule(index)"
          >
            <v-icon size="small">mdi-delete-outline</v-icon>
          </v-btn>
        </div>
        <v-text-field
          v-model.trim="rule.name"
          :label="$t('name')"
          variant="outlined"
          density="compact"
          :disabled="!canWrite || saving"
        />
        <v-row density="compact">
          <v-col cols="12" sm="6">
            <v-select
              v-model="rule.kind"
              :items="kindOptions"
              item-title="text"
              :label="$t('deploymentWindowKind')"
              variant="outlined"
              density="compact"
              :disabled="!canWrite || saving"
            />
          </v-col>
          <v-col cols="12" sm="6">
            <v-select
              v-model="rule.scope"
              :items="scopeOptions"
              item-title="text"
              :label="$t('deploymentWindowScope')"
              variant="outlined"
              density="compact"
              :disabled="!canWrite || saving"
              @update:model-value="clearRuleTarget(rule)"
            />
          </v-col>
        </v-row>
        <v-select
          v-if="rule.scope === 'template'"
          v-model="rule.template_id"
          :items="templates"
          item-title="name"
          item-value="id"
          :label="$t('taskTemplate')"
          variant="outlined"
          density="compact"
          :disabled="!canWrite || saving"
        />
        <v-select
          v-if="rule.scope === 'workflow'"
          v-model="rule.workflow_id"
          :items="workflows"
          item-title="name"
          item-value="id"
          :label="$t('workflow')"
          variant="outlined"
          density="compact"
          :disabled="!canWrite || saving"
        />
        <v-row density="compact">
          <v-col cols="12" sm="8">
            <v-text-field
              v-model.trim="rule.recurrence"
              :label="$t('deploymentWindowRecurrence')"
              hint="0 9 * * 1-5"
              persistent-hint
              variant="outlined"
              density="compact"
              :disabled="!canWrite || saving"
            />
          </v-col>
          <v-col cols="12" sm="4">
            <v-text-field
              v-model.number="rule.duration_minutes"
              type="number"
              min="1"
              max="1440"
              :label="$t('deploymentWindowDuration')"
              variant="outlined"
              density="compact"
              :disabled="!canWrite || saving"
            />
          </v-col>
        </v-row>
        <v-row density="compact">
          <v-col cols="12" sm="6">
            <v-text-field
              v-model="rule.effective_from"
              type="date"
              :label="$t('deploymentWindowEffectiveFrom')"
              variant="outlined"
              density="compact"
              :disabled="!canWrite || saving"
            />
          </v-col>
          <v-col cols="12" sm="6">
            <v-text-field
              v-model="rule.effective_until"
              type="date"
              :label="$t('deploymentWindowEffectiveUntil')"
              variant="outlined"
              density="compact"
              :disabled="!canWrite || saving"
            />
          </v-col>
        </v-row>
      </v-card>

      <div v-if="canWrite" class="d-flex justify-end mb-6">
        <v-btn variant="text" color="error" :disabled="saving" @click="resetDialog = true">
          {{ $t('reset') }}
        </v-btn>
        <v-btn color="primary" :loading="saving" @click="savePolicy">
          {{ $t('save') }}
        </v-btn>
      </div>

      <v-card variant="outlined" class="mb-4">
        <v-card-title class="subtitle-1">{{ $t('deploymentWindowImpactPreview') }}</v-card-title>
        <v-card-text>
          <v-select
            v-model="previewTarget"
            :items="targetOptions"
            item-title="text"
            :label="$t('deploymentWindowTarget')"
            variant="outlined"
            density="compact"
            hide-details
            data-testid="deployment-window-preview-target"
          />
          <div class="d-flex mt-3">
            <v-btn
              v-if="canExecute"
              size="small"
              variant="outlined"
              color="primary"
              :disabled="!previewTarget"
              :loading="previewing"
              @click="previewPolicy"
            >
              {{ $t('preview') }}
            </v-btn>
            <v-btn
              size="small"
              variant="text"
              color="primary"
              :disabled="!previewTarget"
              :loading="checkingStatus"
              @click="loadCurrentStatus"
            >
              {{ $t('deploymentWindowCurrentStatus') }}
            </v-btn>
          </div>
          <v-alert
            v-if="decision"
            class="mt-3 mb-0"
            :type="decision.state === 'blocked' ? 'warning' : 'success'"
            variant="tonal"
            density="compact"
            data-testid="deployment-window-decision"
          >
            <strong>{{ decisionLabel(decision) }}</strong>
            <span v-if="decision.next_eligible_known && decision.next_eligible_at">
              · {{ $t('deploymentWindowNextEligible') }}:
              {{ formatDate(decision.next_eligible_at) }}
            </span>
          </v-alert>
        </v-card-text>
      </v-card>

      <v-card variant="outlined">
        <v-card-title class="subtitle-1">{{ $t('deploymentWindowHistory') }}</v-card-title>
        <v-data-table
          :headers="historyHeaders"
          :items="history"
          :hide-default-footer="true"
          :mobile-breakpoint="720"
          density="compact"
          item-value="id"
          data-testid="deployment-window-history"
        >
          <template v-slot:item.decision="{ item }">
            <v-chip
              size="x-small"
              dark
              :color="item.decision.state === 'blocked' ? 'warning' : 'success'"
            >
              {{ decisionLabel(item.decision) }}
            </v-chip>
          </template>
          <template v-slot:item.target="{ item }">{{ historyTarget(item) }}</template>
          <template v-slot:item.created_at="{ item }">{{ formatDate(item.created_at) }}</template>
          <template v-slot:no-data>
            <div class="py-4 grey--text">{{ $t('deploymentWindowHistoryEmpty') }}</div>
          </template>
        </v-data-table>
      </v-card>
    </template>
  </section>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import YesNoDialog from '@/components/YesNoDialog.vue';

const cleanRule = (rule) => {
  const cleaned = {
    id: Number(rule.id || 0),
    revision: Number(rule.revision),
    name: String(rule.name || '').trim(),
    active: Boolean(rule.active),
    kind: rule.kind,
    scope: rule.scope,
    recurrence: String(rule.recurrence || '').trim(),
    duration_minutes: Number(rule.duration_minutes),
  };
  if (rule.scope === 'template') cleaned.template_id = Number(rule.template_id);
  if (rule.scope === 'workflow') cleaned.workflow_id = Number(rule.workflow_id);
  if (rule.effective_from) cleaned.effective_from = rule.effective_from;
  if (rule.effective_until) cleaned.effective_until = rule.effective_until;
  return cleaned;
};

export default {
  name: 'DeploymentWindowsPanel',
  components: { YesNoDialog },
  props: {
    projectId: { type: Number, required: true },
    capability: { type: Object, required: true },
  },
  data() {
    return {
      policy: null,
      templates: [],
      workflows: [],
      history: [],
      previewTarget: null,
      decision: null,
      loading: false,
      saving: false,
      previewing: false,
      checkingStatus: false,
      resetDialog: false,
      error: '',
    };
  },
  computed: {
    canWrite() {
      return Boolean(this.capability.access?.includes('write'));
    },
    canExecute() {
      return Boolean(this.capability.access?.includes('execute'));
    },
    defaultOptions() {
      return [
        { text: this.$t('deploymentWindowDefaultAllow'), value: 'allow' },
        { text: this.$t('deploymentWindowDefaultDeny'), value: 'deny' },
      ];
    },
    kindOptions() {
      return [
        { text: this.$t('deploymentWindowAllow'), value: 'allow' },
        { text: this.$t('deploymentWindowFreeze'), value: 'freeze' },
      ];
    },
    scopeOptions() {
      return [
        { text: this.$t('project'), value: 'project' },
        { text: this.$t('taskTemplate'), value: 'template' },
        { text: this.$t('workflow'), value: 'workflow' },
      ];
    },
    targetOptions() {
      return [
        ...this.templates.map(({ id, name }) => ({ text: `${this.$t('taskTemplate')}: ${name}`, value: `template:${id}` })),
        ...this.workflows.map(({ id, name }) => ({ text: `${this.$t('workflow')}: ${name}`, value: `workflow:${id}` })),
      ];
    },
    historyHeaders() {
      return [
        { title: this.$t('status'), key: 'decision', sortable: false },
        { title: this.$t('source'), key: 'source', sortable: false },
        { title: this.$t('deploymentWindowTarget'), key: 'target', sortable: false },
        { title: this.$t('created'), key: 'created_at', sortable: false },
      ];
    },
  },
  async created() {
    await this.load();
  },
  methods: {
    async load() {
      this.loading = true;
      this.error = '';
      try {
        const [policy, history, templates, workflows] = await Promise.all([
          axios.get(`/api/project/${this.projectId}/deployment-windows`),
          axios.get(`/api/project/${this.projectId}/deployment-windows/history?count=25`),
          axios.get(`/api/project/${this.projectId}/templates`),
          axios.get(`/api/project/${this.projectId}/workflows`).catch(() => ({ data: [] })),
        ]);
        this.policy = policy.data;
        this.history = history.data || [];
        this.templates = templates.data || [];
        this.workflows = workflows.data || [];
        if (!this.previewTarget && this.targetOptions.length) {
          this.previewTarget = this.targetOptions[0].value;
        }
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.loading = false;
      }
    },
    ruleKey(rule, index) {
      return rule.id > 0 ? `rule-${rule.id}` : `new-rule-${index}`;
    },
    addRule() {
      this.policy.rules.push({
        id: 0,
        revision: this.policy.revision,
        name: '',
        active: true,
        kind: 'allow',
        scope: 'project',
        recurrence: '0 9 * * 1-5',
        duration_minutes: 60,
        effective_from: null,
        effective_until: null,
      });
    },
    removeRule(index) {
      this.policy.rules.splice(index, 1);
    },
    clearRuleTarget(rule) {
      Object.assign(rule, { template_id: null, workflow_id: null });
    },
    policyPayload() {
      return {
        revision: Number(this.policy.revision),
        timezone: String(this.policy.timezone || '').trim(),
        default: this.policy.default,
        rules: (this.policy.rules || []).map(cleanRule),
      };
    },
    targetInput() {
      if (!this.previewTarget) return {};
      const [kind, rawID] = this.previewTarget.split(':');
      const id = Number(rawID);
      if (kind === 'template') return { template_id: id };
      if (kind === 'workflow') return { workflow_id: id };
      return {};
    },
    previewPayload() {
      return { ...this.policyPayload(), ...this.targetInput() };
    },
    async savePolicy() {
      this.saving = true;
      this.error = '';
      try {
        this.policy = (await axios.put(
          `/api/project/${this.projectId}/deployment-windows`,
          this.policyPayload(),
        )).data;
      } catch (err) {
        this.error = err?.response?.status === 409
          ? this.$t('deploymentWindowRevisionConflict')
          : getErrorMessage(err);
      } finally {
        this.saving = false;
      }
    },
    async resetPolicy() {
      this.resetDialog = false;
      this.saving = true;
      this.error = '';
      try {
        await axios.delete(`/api/project/${this.projectId}/deployment-windows`, {
          params: { expected_revision: this.policy.revision },
        });
        await this.load();
      } catch (err) {
        this.error = err?.response?.status === 409
          ? this.$t('deploymentWindowRevisionConflict')
          : getErrorMessage(err);
      } finally {
        this.saving = false;
      }
    },
    async previewPolicy() {
      this.previewing = true;
      this.error = '';
      try {
        this.decision = (await axios.post(
          `/api/project/${this.projectId}/deployment-windows/preview`,
          this.previewPayload(),
        )).data;
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.previewing = false;
      }
    },
    async loadCurrentStatus() {
      this.checkingStatus = true;
      this.error = '';
      try {
        this.decision = (await axios.get(
          `/api/project/${this.projectId}/deployment-windows/status`,
          { params: this.targetInput() },
        )).data;
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.checkingStatus = false;
      }
    },
    decisionLabel(decision) {
      return this.$t(`deploymentWindowDecision_${decision.state}_${decision.reason}`);
    },
    historyTarget(item) {
      if (item.template_id) return `${this.$t('taskTemplate')} #${item.template_id}`;
      if (item.workflow_id) return `${this.$t('workflow')} #${item.workflow_id}`;
      return this.$t('project');
    },
    formatDate(value) {
      return value ? new Date(value).toLocaleString() : '—';
    },
  },
};
</script>
