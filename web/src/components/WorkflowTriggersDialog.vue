<template>
  <v-dialog
    v-model="open"
    :fullscreen="$vuetify.breakpoint.xsOnly"
    max-width="960"
    scrollable
  >
    <v-card data-testid="workflow-triggers">
      <v-card-title class="d-flex align-center">
        <span>{{ $t('workflowTriggers') }}</span>
        <v-spacer />
        <v-btn icon :title="$t('close')" @click="open = false">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-alert
        v-if="credential"
        type="warning"
        text
        class="mx-4 mb-2"
        data-testid="workflow-trigger-credential"
      >
        <div class="font-weight-medium">{{ $t('workflowTriggerCredentialOnce') }}</div>
        <v-text-field
          :value="credential"
          readonly
          hide-details
          class="mt-2"
          @focus="$event.target.select()"
        />
        <v-btn text small class="mt-2" @click="credential = ''">
          {{ $t('dismiss') }}
        </v-btn>
      </v-alert>

      <v-card-text>
        <v-alert
          v-if="!capabilityAllowsWrite"
          type="info"
          text
          data-testid="workflow-trigger-capability"
        >
          {{ $t('workflowTriggersUnavailable') }}
        </v-alert>

        <div class="d-flex align-center mb-3">
          <span class="text-body-2">{{ $t('workflowTriggersHint') }}</span>
          <v-spacer />
          <v-btn
            color="primary"
            depressed
            :disabled="!capabilityAllowsWrite"
            data-testid="workflow-trigger-add"
            @click="beginCreate"
          >
            <v-icon left>mdi-plus</v-icon>
            {{ $t('add') }}
          </v-btn>
        </div>

        <v-progress-linear v-if="loading" indeterminate color="primary" />
        <div
          v-else-if="triggers.length && $vuetify.breakpoint.xsOnly"
          class="WorkflowTriggersDialog__mobile"
        >
          <v-card
            v-for="trigger in triggers"
            :key="trigger.id"
            outlined
            class="pa-3 mb-3"
          >
            <div class="d-flex align-center">
              <div>
                <div class="font-weight-medium">{{ trigger.name }}</div>
                <div class="text-caption">
                  {{ triggerTypeLabel(trigger.type) }}
                  <span v-if="trigger.cron_format"> · {{ trigger.cron_format }}</span>
                </div>
              </div>
              <v-spacer />
              <v-chip x-small :color="trigger.enabled ? 'success' : 'grey'" dark>
                {{ trigger.enabled ? $t('enabled') : $t('disabled') }}
              </v-chip>
            </div>
            <div class="text-caption mt-2">
              {{ $t('workflowTriggerLastResult') }}:
              {{ trigger.last_result || $t('never') }}
              <span v-if="trigger.last_fired"> · {{ formatDate(trigger.last_fired) }}</span>
            </div>
            <div class="d-flex flex-wrap justify-end mt-2 WorkflowTriggersDialog__actions">
              <v-btn
                icon small :title="$t('edit')" :disabled="!capabilityAllowsWrite"
                @click="beginEdit(trigger)"
              ><v-icon small>mdi-pencil</v-icon></v-btn>
              <v-btn
                icon small :title="trigger.enabled ? $t('disable') : $t('enable')"
                :disabled="!capabilityAllowsWrite || mutating"
                @click="setEnabled(trigger, !trigger.enabled)"
              ><v-icon small>{{ trigger.enabled ? 'mdi-pause' : 'mdi-play' }}</v-icon></v-btn>
              <v-btn
                v-if="usesCredential(trigger)" icon small
                :title="$t('workflowTriggerRotateCredential')"
                :disabled="!capabilityAllowsWrite || mutating"
                @click="rotate(trigger)"
              ><v-icon small>mdi-key-sync</v-icon></v-btn>
              <v-btn
                icon small :title="$t('workflowTriggerTest')"
                :disabled="!capabilityAllowsExecute || !trigger.enabled || mutating"
                @click="beginTest(trigger)"
              ><v-icon small>mdi-play-circle-outline</v-icon></v-btn>
              <v-btn
                icon small :title="$t('workflowTriggerHistory')"
                @click="showHistory(trigger)"
              ><v-icon small>mdi-history</v-icon></v-btn>
              <v-btn
                icon small color="error" :title="$t('delete')"
                :disabled="!capabilityAllowsWrite || mutating"
                @click="pendingDelete = trigger"
              ><v-icon small>mdi-delete</v-icon></v-btn>
            </div>
          </v-card>
        </div>
        <v-simple-table v-else-if="triggers.length" class="WorkflowTriggersDialog__table">
          <thead>
            <tr>
              <th>{{ $t('name') }}</th>
              <th>{{ $t('type') }}</th>
              <th>{{ $t('status') }}</th>
              <th>{{ $t('workflowTriggerLastResult') }}</th>
              <th class="text-right">{{ $t('actions') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="trigger in triggers" :key="trigger.id">
              <td>
                <div class="font-weight-medium">{{ trigger.name }}</div>
                <div v-if="trigger.cron_format" class="text-caption">
                  {{ trigger.cron_format }}
                </div>
              </td>
              <td>{{ triggerTypeLabel(trigger.type) }}</td>
              <td>
                <v-chip x-small :color="trigger.enabled ? 'success' : 'grey'" dark>
                  {{ trigger.enabled ? $t('enabled') : $t('disabled') }}
                </v-chip>
              </td>
              <td>
                <div>{{ trigger.last_result || $t('never') }}</div>
                <div v-if="trigger.last_fired" class="text-caption">
                  {{ formatDate(trigger.last_fired) }}
                </div>
              </td>
              <td class="text-right WorkflowTriggersDialog__actions">
                <v-btn
                  icon
                  small
                  :title="$t('edit')"
                  :disabled="!capabilityAllowsWrite"
                  @click="beginEdit(trigger)"
                ><v-icon small>mdi-pencil</v-icon></v-btn>
                <v-btn
                  icon
                  small
                  :title="trigger.enabled ? $t('disable') : $t('enable')"
                  :disabled="!capabilityAllowsWrite || mutating"
                  @click="setEnabled(trigger, !trigger.enabled)"
                ><v-icon small>{{ trigger.enabled ? 'mdi-pause' : 'mdi-play' }}</v-icon></v-btn>
                <v-btn
                  v-if="usesCredential(trigger)"
                  icon
                  small
                  :title="$t('workflowTriggerRotateCredential')"
                  :disabled="!capabilityAllowsWrite || mutating"
                  @click="rotate(trigger)"
                ><v-icon small>mdi-key-sync</v-icon></v-btn>
                <v-btn
                  icon
                  small
                  :title="$t('workflowTriggerTest')"
                  :disabled="!capabilityAllowsExecute || !trigger.enabled || mutating"
                  @click="beginTest(trigger)"
                ><v-icon small>mdi-play-circle-outline</v-icon></v-btn>
                <v-btn
                  icon
                  small
                  :title="$t('workflowTriggerHistory')"
                  @click="showHistory(trigger)"
                ><v-icon small>mdi-history</v-icon></v-btn>
                <v-btn
                  icon
                  small
                  color="error"
                  :title="$t('delete')"
                  :disabled="!capabilityAllowsWrite || mutating"
                  @click="pendingDelete = trigger"
                ><v-icon small>mdi-delete</v-icon></v-btn>
              </td>
            </tr>
          </tbody>
        </v-simple-table>
        <v-alert v-else text type="info">{{ $t('workflowTriggerNone') }}</v-alert>
      </v-card-text>
    </v-card>

    <v-dialog v-model="formDialog" max-width="720" scrollable>
      <v-card data-testid="workflow-trigger-form">
        <v-card-title>
          {{ editingId ? $t('workflowTriggerEdit') : $t('workflowTriggerAdd') }}
        </v-card-title>
        <v-card-text>
          <v-text-field v-model="form.name" :label="$t('name')" maxlength="128" />
          <v-select
            v-model="form.type"
            :items="triggerTypes"
            :label="$t('type')"
            :disabled="editingId != null"
          />
          <v-switch v-model="form.enabled" :label="$t('enabled')" />
          <v-text-field
            v-if="form.type === 'schedule'"
            v-model="form.cron_format"
            :label="$t('workflowTriggerCron')"
            :hint="$t('workflowTriggerCronHint')"
            persistent-hint
          />

          <div v-if="parameters.length" class="mt-4">
            <div class="text-subtitle-2 mb-2">{{ $t('workflowTriggerInputs') }}</div>
            <div
              v-for="parameter in parameters"
              :key="parameter.name"
              class="WorkflowTriggersDialog__mapping mb-3"
            >
              <div class="text-body-2 font-weight-medium mb-1">
                {{ parameter.name }}
                <span v-if="parameter.required" class="error--text">*</span>
              </div>
              <v-select
                v-model="form.sources[parameter.name]"
                :items="mappingSources()"
                :label="$t('workflowTriggerInputSource')"
                dense
                hide-details
              />
              <v-text-field
                v-if="form.sources[parameter.name] === 'request'"
                v-model="form.requestKeys[parameter.name]"
                :label="$t('workflowTriggerRequestField')"
                dense
              />
              <v-select
                v-else-if="form.sources[parameter.name] === 'fixed'
                  && parameter.type === 'enumeration'"
                v-model="form.fixedValues[parameter.name]"
                :items="parameter.options || []"
                :label="$t('value')"
                dense
              />
              <v-select
                v-else-if="form.sources[parameter.name] === 'fixed'
                  && parameter.type === 'boolean'"
                v-model="form.fixedValues[parameter.name]"
                :items="booleanValues"
                :label="$t('value')"
                dense
              />
              <v-select
                v-else-if="form.sources[parameter.name] === 'fixed'
                  && parameter.type === 'secret_reference'"
                v-model="form.fixedValues[parameter.name]"
                :items="secretOptions(parameter)"
                :label="$t('workflowTriggerCredentialReference')"
                dense
              />
              <v-text-field
                v-else-if="form.sources[parameter.name] === 'fixed'"
                v-model="form.fixedValues[parameter.name]"
                :type="parameter.type === 'integer' ? 'number' : 'text'"
                :label="$t('value')"
                dense
              />
            </div>
          </div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="formDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn
            color="primary"
            depressed
            :loading="mutating"
            :disabled="!form.name || !form.type || (form.type === 'schedule' && !form.cron_format)"
            data-testid="workflow-trigger-save"
            @click="save"
          >{{ $t('save') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="testDialog" max-width="560">
      <v-card data-testid="workflow-trigger-test-form">
        <v-card-title>{{ $t('workflowTriggerTest') }}</v-card-title>
        <v-card-text>
          <div v-if="testRequestMappings.length === 0" class="text-body-2">
            {{ $t('workflowTriggerTestNoInputs') }}
          </div>
          <template v-for="mapping in testRequestMappings">
            <v-select
              v-if="mapping.parameterDefinition.type === 'boolean'"
              :key="mapping.key"
              v-model="testValues[mapping.key]"
              :items="booleanValues"
              :label="mapping.key"
            />
            <v-select
              v-else-if="mapping.parameterDefinition.type === 'enumeration'"
              :key="mapping.key"
              v-model="testValues[mapping.key]"
              :items="mapping.parameterDefinition.options || []"
              :label="mapping.key"
            />
            <v-select
              v-else-if="mapping.parameterDefinition.type === 'secret_reference'"
              :key="mapping.key"
              v-model="testValues[mapping.key]"
              :items="secretOptions(mapping.parameterDefinition)"
              :label="mapping.key"
            />
            <v-text-field
              v-else-if="mapping.parameterDefinition.type === 'integer'"
              :key="mapping.key"
              v-model.number="testValues[mapping.key]"
              type="number"
              :min="mapping.parameterDefinition.minimum"
              :max="mapping.parameterDefinition.maximum"
              :label="mapping.key"
            />
            <v-text-field
              v-else
              :key="mapping.key"
              v-model="testValues[mapping.key]"
              :label="mapping.key"
            />
          </template>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="testDialog = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" :loading="mutating" @click="testFire">
            {{ $t('workflowTriggerTest') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="historyDialog" max-width="720" scrollable>
      <v-card data-testid="workflow-trigger-history">
        <v-card-title>{{ $t('workflowTriggerHistory') }}</v-card-title>
        <v-card-text>
          <v-progress-linear v-if="historyLoading" indeterminate />
          <v-list v-else-if="history.length" two-line>
            <v-list-item v-for="entry in history" :key="entry.id">
              <v-list-item-content>
                <v-list-item-title>{{ entry.result || entry.status }}</v-list-item-title>
                <v-list-item-subtitle>
                  {{ formatDate(entry.created) }}
                  <span v-if="entry.run_id"> · Run #{{ entry.run_id }}</span>
                </v-list-item-subtitle>
                <v-list-item-subtitle v-if="entry.reason">
                  {{ entry.reason }}
                </v-list-item-subtitle>
              </v-list-item-content>
            </v-list-item>
          </v-list>
          <v-alert v-else text type="info">{{ $t('workflowTriggerHistoryEmpty') }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="historyDialog = false">{{ $t('close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <YesNoDialog
      v-model="deleteDialog"
      :title="$t('workflowTriggerDelete')"
      :text="$t('workflowTriggerDeleteConfirm')"
      @yes="remove"
    />
  </v-dialog>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { findCapabilityDecision } from '@/lib/capabilities';
import { getErrorMessage } from '@/lib/error';
import YesNoDialog from '@/components/YesNoDialog.vue';

function emptyForm() {
  return {
    name: '',
    type: 'manual',
    enabled: true,
    cron_format: '',
    revision: 0,
    sources: {},
    requestKeys: {},
    fixedValues: {},
  };
}

export default {
  components: { YesNoDialog },

  props: {
    value: Boolean,
    projectId: { type: Number, required: true },
    workflow: { type: Object, required: true },
    systemInfo: Object,
  },

  data() {
    return {
      triggers: [],
      loading: false,
      mutating: false,
      credential: '',
      formDialog: false,
      editingId: null,
      form: emptyForm(),
      testDialog: false,
      testingTrigger: null,
      testValues: {},
      historyDialog: false,
      historyLoading: false,
      history: [],
      pendingDelete: null,
      triggerTypes: [
        { value: 'manual', text: this.$t('workflowTriggerTypeManual') },
        { value: 'schedule', text: this.$t('workflowTriggerTypeSchedule') },
        { value: 'api', text: this.$t('workflowTriggerTypeAPI') },
        { value: 'webhook', text: this.$t('workflowTriggerTypeWebhook') },
      ],
      booleanValues: [
        { value: true, text: this.$t('yes') },
        { value: false, text: this.$t('no') },
      ],
    };
  },

  computed: {
    open: {
      get() { return this.value; },
      set(value) { this.$emit('input', value); },
    },

    parameters() {
      return this.workflow.parameters || [];
    },

    capabilityDecision() {
      return findCapabilityDecision(this.systemInfo, 'workflow_triggers');
    },

    capabilityAllowsWrite() {
      return !this.capabilityDecision
        || (this.capabilityDecision.access || []).includes('write');
    },

    capabilityAllowsExecute() {
      return !this.capabilityDecision
        || (this.capabilityDecision.access || []).includes('execute');
    },

    deleteDialog: {
      get() { return this.pendingDelete != null; },
      set(value) {
        if (!value) this.pendingDelete = null;
      },
    },

    testRequestMappings() {
      return (this.testingTrigger?.input_mappings || [])
        .filter(({ source }) => source === 'request')
        .map((mapping) => ({
          ...mapping,
          parameterDefinition: this.parameters.find(({ name }) => name === mapping.parameter),
        }))
        .filter(({ parameterDefinition }) => parameterDefinition != null);
    },

    baseURL() {
      return `/api/project/${this.projectId}/workflows/${this.workflow.id}/triggers`;
    },
  },

  watch: {
    async value(value) {
      if (value) {
        this.credential = '';
        await this.load();
      }
    },
  },

  methods: {
    async load() {
      this.loading = true;
      try {
        this.triggers = (await axios.get(this.baseURL)).data || [];
      } catch (err) {
        this.notifyError(err);
      } finally {
        this.loading = false;
      }
    },

    beginCreate() {
      this.editingId = null;
      this.form = emptyForm();
      this.initializeMappings([]);
      this.formDialog = true;
    },

    beginEdit(trigger) {
      this.editingId = trigger.id;
      this.form = {
        ...emptyForm(),
        name: trigger.name,
        type: trigger.type,
        enabled: trigger.enabled,
        cron_format: trigger.cron_format || '',
        revision: trigger.revision,
      };
      this.initializeMappings(trigger.input_mappings || []);
      this.formDialog = true;
    },

    initializeMappings(mappings) {
      this.parameters.forEach((parameter) => {
        const mapping = mappings.find(({ parameter: name }) => name === parameter.name);
        const source = mapping?.source || (parameter.required ? 'fixed' : 'none');
        this.$set(this.form.sources, parameter.name, source);
        this.$set(this.form.requestKeys, parameter.name, mapping?.key || parameter.name);
        this.$set(
          this.form.fixedValues,
          parameter.name,
          mapping?.value ?? this.initialFixedValue(parameter),
        );
      });
    },

    initialFixedValue(parameter) {
      if (parameter.default !== undefined) return parameter.default;
      if (parameter.type === 'boolean') return false;
      if (parameter.type === 'integer') return 0;
      if (parameter.type === 'enumeration') return (parameter.options || [])[0];
      if (parameter.type === 'secret_reference') {
        const first = (parameter.secret_options || [])[0];
        return first ? { access_key_id: first.access_key_id } : null;
      }
      return '';
    },

    mappingSources() {
      const sources = [
        { value: 'none', text: this.$t('workflowTriggerInputUnmapped') },
        { value: 'fixed', text: this.$t('workflowTriggerInputFixed') },
      ];
      if (this.form.type !== 'schedule') {
        sources.push({ value: 'request', text: this.$t('workflowTriggerInputRequest') });
      }
      return sources;
    },

    secretOptions(parameter) {
      return (parameter.secret_options || []).map((option) => ({
        value: { access_key_id: option.access_key_id },
        text: option.label || `#${option.access_key_id}`,
      }));
    },

    payload() {
      const inputMappings = [];
      this.parameters.forEach((parameter) => {
        const source = this.form.sources[parameter.name];
        if (source === 'fixed') {
          inputMappings.push({
            parameter: parameter.name,
            source,
            value: this.fixedValue(parameter),
          });
        } else if (source === 'request') {
          inputMappings.push({
            parameter: parameter.name,
            source,
            key: this.form.requestKeys[parameter.name],
          });
        }
      });
      return {
        name: this.form.name,
        type: this.form.type,
        enabled: this.form.enabled,
        cron_format: this.form.type === 'schedule' ? this.form.cron_format : '',
        revision: this.form.revision,
        input_mappings: inputMappings,
      };
    },

    fixedValue(parameter) {
      const value = this.form.fixedValues[parameter.name];
      if (parameter.type === 'integer') return Number(value);
      return value;
    },

    async save() {
      this.mutating = true;
      try {
        if (this.editingId == null) {
          const result = (await axios.post(this.baseURL, this.payload())).data;
          this.credential = result.credential || '';
        } else {
          await axios.put(`${this.baseURL}/${this.editingId}`, this.payload());
        }
        this.formDialog = false;
        await this.load();
      } catch (err) {
        this.notifyError(err);
      } finally {
        this.mutating = false;
      }
    },

    async setEnabled(trigger, enabled) {
      await this.mutate(async () => {
        await axios.put(`${this.baseURL}/${trigger.id}/enabled`, {
          revision: trigger.revision,
          enabled,
        });
      });
    },

    async rotate(trigger) {
      await this.mutate(async () => {
        const result = (await axios.post(`${this.baseURL}/${trigger.id}/rotate`, {
          revision: trigger.revision,
        })).data;
        this.credential = result.credential || '';
      });
    },

    beginTest(trigger) {
      this.testingTrigger = trigger;
      this.testValues = {};
      this.testRequestMappings.forEach((mapping) => {
        this.$set(
          this.testValues,
          mapping.key,
          this.initialFixedValue(mapping.parameterDefinition),
        );
      });
      this.testDialog = true;
    },

    async testFire() {
      await this.mutate(async () => {
        const result = (await axios.post(
          `${this.baseURL}/${this.testingTrigger.id}/test`,
          { inputs: this.testValues },
        )).data;
        this.testDialog = false;
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('workflowTriggerTestStarted', { id: result.run.id }),
        });
      });
    },

    async showHistory(trigger) {
      this.historyDialog = true;
      this.historyLoading = true;
      try {
        this.history = (await axios.get(`${this.baseURL}/${trigger.id}/history?count=50`)).data || [];
      } catch (err) {
        this.notifyError(err);
      } finally {
        this.historyLoading = false;
      }
    },

    async remove() {
      const trigger = this.pendingDelete;
      this.pendingDelete = null;
      if (!trigger) return;
      await this.mutate(async () => {
        await axios.delete(`${this.baseURL}/${trigger.id}`);
      });
    },

    async mutate(operation) {
      this.mutating = true;
      try {
        await operation();
        await this.load();
      } catch (err) {
        this.notifyError(err);
      } finally {
        this.mutating = false;
      }
    },

    usesCredential(trigger) {
      return trigger.type === 'api' || trigger.type === 'webhook';
    },

    triggerTypeLabel(type) {
      const item = this.triggerTypes.find(({ value }) => value === type);
      return item ? item.text : type;
    },

    formatDate(value) {
      return value ? new Date(value).toLocaleString() : '';
    },

    notifyError(err) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
    },
  },
};
</script>

<style scoped>
.WorkflowTriggersDialog__actions {
  white-space: nowrap;
}

.WorkflowTriggersDialog__mapping {
  border-left: 3px solid var(--v-primary-base);
  padding-left: 12px;
}
</style>
