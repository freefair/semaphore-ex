<template>
  <v-dialog :value="value" max-width="720" scrollable @input="$emit('input', $event)">
    <v-card data-testid="workflow-run-inputs">
      <v-card-title>{{ $t('workflowRunInputs') }}</v-card-title>
      <v-card-text>
        <v-alert v-if="preflightMessage" type="warning" dense text>
          {{ preflightMessage }}
        </v-alert>
        <v-alert
          v-if="deploymentWindowBlock"
          type="warning"
          dense
          text
          data-testid="deployment-window-blocked"
        >
          <div>{{ deploymentWindowBlockMessage }}</div>
          <div
            v-if="deploymentWindowBlock.next_eligible_known
              && deploymentWindowBlock.next_eligible_at"
            class="text-caption mt-1"
          >
            {{ $t('deploymentWindowNextEligible') }}:
            {{ formatDeploymentWindowDate(deploymentWindowBlock.next_eligible_at) }}
          </div>
        </v-alert>
        <v-alert v-if="deploymentWindowOverrideError" type="error" dense text>
          {{ deploymentWindowOverrideError }}
        </v-alert>
        <v-alert v-for="error in errors" :key="error" type="error" dense text>
          {{ error }}
        </v-alert>

        <template v-if="parameters.length">
          <div class="text-subtitle-2 mb-2">{{ $t('workflowParameters') }}</div>
          <template v-for="parameter in parameters">
            <v-text-field
              v-if="parameter.type === 'string'"
              :key="parameter.name"
              v-model="values[parameter.name]"
              :label="parameterLabel(parameter)"
              :hint="parameterHint(parameter)"
              persistent-hint
              clearable
              outlined
              dense
            />
            <v-text-field
              v-else-if="parameter.type === 'integer'"
              :key="parameter.name"
              v-model.number="values[parameter.name]"
              type="number"
              :min="parameter.minimum"
              :max="parameter.maximum"
              :label="parameterLabel(parameter)"
              :hint="parameterHint(parameter)"
              persistent-hint
              clearable
              outlined
              dense
            />
            <v-select
              v-else-if="parameter.type === 'boolean'"
              :key="parameter.name"
              v-model="values[parameter.name]"
              :items="booleanOptions"
              item-value="value"
              item-text="text"
              :label="parameterLabel(parameter)"
              :hint="parameterHint(parameter)"
              persistent-hint
              clearable
              outlined
              dense
            />
            <v-select
              v-else-if="parameter.type === 'enumeration'"
              :key="parameter.name"
              v-model="values[parameter.name]"
              :items="parameter.options || []"
              :label="parameterLabel(parameter)"
              :hint="parameterHint(parameter)"
              persistent-hint
              clearable
              outlined
              dense
            />
            <v-select
              v-else-if="parameter.type === 'secret_reference'"
              :key="parameter.name"
              v-model="values[parameter.name]"
              :items="secretOptions(parameter)"
              item-value="value"
              item-text="text"
              :label="parameterLabel(parameter)"
              :hint="parameterHint(parameter)"
              persistent-hint
              clearable
              outlined
              dense
            />
          </template>
        </template>

        <template v-if="configurableNodes.length">
          <div class="text-subtitle-2 mt-3 mb-2">{{ $t('workflowNodeOverrides') }}</div>
          <v-card
            v-for="node in configurableNodes"
            :key="`run-node-${node.id}`"
            outlined
            class="pa-3 mb-3"
          >
            <div class="text-body-2 font-weight-medium mb-2">
              #{{ node.id }} {{ node.display_name }}
            </div>
            <v-select
              v-if="node.override_policy.inventory_ids.length"
              v-model="nodeValues[node.id].inventory_id"
              :items="allowedInventories(node)"
              item-value="id"
              item-text="name"
              :label="$t('inventory')"
              clearable
              outlined
              dense
            />
            <v-select
              v-if="node.override_policy.environment_ids.length"
              v-model="nodeValues[node.id].environment_ids"
              :items="allowedEnvironments(node)"
              item-value="id"
              item-text="name"
              :label="$t('environment')"
              multiple
              chips
              small-chips
              clearable
              outlined
              dense
            />
            <v-text-field
              v-if="node.override_policy.allow_arguments"
              v-model="nodeValues[node.id].arguments"
              :label="$t('workflowArgumentsOverride')"
              :hint="$t('workflowArgumentsOverrideHint')"
              persistent-hint
              clearable
              outlined
              dense
            />
            <v-text-field
              v-if="node.override_policy.allow_branch"
              v-model="nodeValues[node.id].git_branch"
              :label="$t('branch')"
              clearable
              outlined
              dense
            />
          </v-card>
        </template>

        <template v-if="deploymentWindowBlock">
          <div class="text-subtitle-2 mt-3 mb-2">
            {{ $t('deploymentWindowEmergencyOverride') }}
          </div>
          <v-select
            v-model="deploymentWindowOverrideCategory"
            :items="deploymentWindowOverrideCategories"
            :label="$t('deploymentWindowOverrideCategory')"
            outlined
            dense
            :disabled="busy"
          />
          <v-text-field
            v-model.trim="deploymentWindowOverrideReference"
            :label="$t('deploymentWindowOverrideReference')"
            :hint="$t('deploymentWindowOverrideReferenceHint')"
            persistent-hint
            outlined
            dense
            :disabled="busy"
            data-testid="deployment-window-override-reference"
          />
          <v-checkbox
            v-model="deploymentWindowOverrideConfirmed"
            :label="$t('deploymentWindowOverrideConfirm')"
            :disabled="busy"
            data-testid="deployment-window-override-confirm"
          />
        </template>

        <ExecutionPreflightReview :plan="executionPreflight" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn text :disabled="busy" @click="$emit('input', false)">
          {{ $t('cancel') }}
        </v-btn>
        <v-btn
          color="primary"
          :loading="busy"
          :disabled="errors.length > 0 || hasDenial"
          @click="submit"
        >
          {{ executionPreflight ? $t('confirmExecution') : $t('run') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import {
  credentialOptionItems,
  credentialReferenceFromKey,
} from '@/lib/workflow-credential-references';
import ExecutionPreflightReview from '@/components/ExecutionPreflightReview.vue';

const NODE_OVERRIDE_FIELDS = ['inventory_id', 'environment_ids', 'arguments', 'git_branch'];
const stringByteLength = (value) => new TextEncoder().encode(String(value)).length;

export default {
  components: { ExecutionPreflightReview },
  props: {
    value: Boolean,
    workflow: { type: Object, required: true },
    projectId: { type: Number, required: true },
    loading: Boolean,
  },
  data() {
    return {
      values: {},
      nodeValues: {},
      inventories: [],
      environments: [],
      executionPreflight: null,
      executionPreflightPayloadSignature: null,
      preflightLoading: false,
      preflightMessage: null,
      deploymentWindowBlock: null,
      deploymentWindowOverrideCategory: null,
      deploymentWindowOverrideReference: '',
      deploymentWindowOverrideConfirmed: false,
      deploymentWindowOverrideError: '',
    };
  },
  computed: {
    parameters() {
      return this.workflow.parameters || [];
    },
    configurableNodes() {
      return (this.workflow.nodes || []).filter((node) => {
        const policy = node.override_policy || {};
        return (policy.inventory_ids || []).length
          || (policy.environment_ids || []).length
          || policy.allow_arguments
          || policy.allow_branch;
      }).map((node) => ({
        ...node,
        override_policy: {
          inventory_ids: [], environment_ids: [], ...node.override_policy,
        },
      }));
    },
    booleanOptions() {
      return [
        { value: true, text: this.$t('yes') },
        { value: false, text: this.$t('workflowBooleanFalse') },
      ];
    },
    busy() {
      return this.loading || this.preflightLoading;
    },
    hasDenial() {
      return (this.executionPreflight?.findings || []).some(({ severity }) => severity === 'denial');
    },
    deploymentWindowOverrideCategories() {
      return [
        { text: this.$t('deploymentWindowOverrideIncident'), value: 'incident' },
        { text: this.$t('deploymentWindowOverrideSecurity'), value: 'security' },
        { text: this.$t('deploymentWindowOverrideCustomerImpact'), value: 'customer_impact' },
      ];
    },
    deploymentWindowOverrideReady() {
      return Boolean(this.deploymentWindowBlock
        && this.deploymentWindowOverrideConfirmed
        && ['incident', 'security', 'customer_impact']
          .includes(this.deploymentWindowOverrideCategory)
        && /^[A-Z0-9]{2,16}-[1-9][0-9]{0,9}$/
          .test(this.deploymentWindowOverrideReference));
    },
    deploymentWindowBlockMessage() {
      return this.$t(`deploymentWindowBlocked_${this.deploymentWindowBlock?.reason || 'unknown'}`);
    },
    errors() {
      const errors = [];
      this.parameters.forEach((parameter) => {
        const value = this.values[parameter.name];
        const missing = value === undefined || value === null
          || (value === '' && parameter.type !== 'string');
        const hasDefault = parameter.default !== undefined && parameter.default !== null;
        if (parameter.required && missing && !hasDefault) {
          errors.push(this.$t('workflowParameterRequired', { name: parameter.name }));
          return;
        }
        if (missing) return;
        if (parameter.type === 'string'
          && ((parameter.min_length && stringByteLength(value) < parameter.min_length)
            || (parameter.max_length && stringByteLength(value) > parameter.max_length))) {
          errors.push(this.$t('workflowParameterStringBounds', { name: parameter.name }));
        }
        if (parameter.type === 'integer'
          && (!Number.isSafeInteger(Number(value))
            || (parameter.minimum != null && Number(value) < parameter.minimum)
            || (parameter.maximum != null && Number(value) > parameter.maximum))) {
          errors.push(this.$t('workflowParameterIntegerBounds', { name: parameter.name }));
        }
      });
      return errors;
    },
  },
  watch: {
    value: {
      immediate: true,
      handler(open) {
        if (open) {
          this.reset();
          if (!this.parameters.length && !this.configurableNodes.length) {
            this.$nextTick(() => this.submit());
          }
        }
      },
    },
  },
  async created() {
    try {
      const [inventories, environments] = await Promise.all([
        axios.get(`/api/project/${this.projectId}/inventory`),
        axios.get(`/api/project/${this.projectId}/environment`),
      ]);
      this.inventories = inventories.data || [];
      this.environments = environments.data || [];
    } catch (err) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
    }
  },
  methods: {
    stringByteLength,
    reset() {
      this.values = this.parameters.reduce((values, parameter) => ({
        ...values, [parameter.name]: undefined,
      }), {});
      this.nodeValues = this.configurableNodes.reduce((values, node) => ({
        ...values,
        [node.id]: {
          inventory_id: undefined,
          environment_ids: undefined,
          arguments: undefined,
          git_branch: undefined,
        },
      }), {});
      this.executionPreflight = null;
      this.executionPreflightPayloadSignature = null;
      this.preflightMessage = null;
      this.clearDeploymentWindowBlock();
    },
    parameterLabel(parameter) {
      return `${parameter.name}${parameter.required ? ' *' : ''}`;
    },
    parameterHint(parameter) {
      const parts = [];
      if (parameter.description) parts.push(parameter.description);
      if (parameter.default !== undefined && parameter.default !== null) {
        const value = parameter.type === 'secret_reference'
          ? this.$t('workflowSecretReferenceDefault')
          : JSON.stringify(parameter.default);
        parts.push(this.$t('workflowDefaultHint', { value }));
      }
      return parts.join(' · ');
    },
    allowedInventories(node) {
      const ids = node.override_policy.inventory_ids;
      return ids.map((id) => this.inventories.find((entry) => entry.id === id)
        || { id, name: `#${id}` });
    },
    allowedEnvironments(node) {
      const ids = node.override_policy.environment_ids;
      return ids.map((id) => this.environments.find((entry) => entry.id === id)
        || { id, name: `#${id}` });
    },
    secretOptions(parameter) {
      return credentialOptionItems(parameter.secret_options);
    },
    buildPayload() {
      const parameters = {};
      (this.workflow.parameters || []).forEach((parameter) => {
        const value = this.values[parameter.name];
        if (value === undefined || value === null
          || (value === '' && parameter.type !== 'string')) return;
        if (parameter.type === 'secret_reference') {
          const reference = credentialReferenceFromKey(value);
          if (reference) parameters[parameter.name] = reference;
        } else {
          parameters[parameter.name] = value;
        }
      });
      const nodeOverrides = {};
      (this.workflow.nodes || []).forEach((node) => {
        const policy = node.override_policy || {};
        const values = this.nodeValues[node.id] || {};
        const override = {};
        NODE_OVERRIDE_FIELDS.forEach((field) => {
          const value = values[field];
          if (value === undefined || value === null || value === '') return;
          if (field === 'inventory_id' && !(policy.inventory_ids || []).includes(value)) return;
          if (field === 'environment_ids'
            && (!Array.isArray(value)
              || value.some((id) => !(policy.environment_ids || []).includes(id)))) return;
          if (field === 'arguments' && !policy.allow_arguments) return;
          if (field === 'git_branch' && !policy.allow_branch) return;
          override[field] = value;
        });
        if (Object.keys(override).length) nodeOverrides[node.id] = override;
      });
      const payload = {};
      if (Object.keys(parameters).length) payload.parameters = parameters;
      if (Object.keys(nodeOverrides).length) payload.node_overrides = nodeOverrides;
      return payload;
    },
    buildStartPayload(payload) {
      if (!this.deploymentWindowOverrideReady) return payload;
      return {
        ...payload,
        deployment_window_override: {
          category: this.deploymentWindowOverrideCategory,
          reference: this.deploymentWindowOverrideReference,
        },
      };
    },
    isDeploymentWindowBlock(err) {
      return err?.response?.status === 409
        && err?.response?.data?.state === 'blocked';
    },
    adoptDeploymentWindowBlock(decision) {
      this.deploymentWindowBlock = decision;
      this.deploymentWindowOverrideCategory = null;
      this.deploymentWindowOverrideReference = '';
      this.deploymentWindowOverrideConfirmed = false;
      this.deploymentWindowOverrideError = '';
      this.preflightMessage = null;
    },
    setDeploymentWindowOverrideError() {
      this.deploymentWindowOverrideError = this.$t('deploymentWindowOverrideForbidden');
    },
    clearDeploymentWindowBlock() {
      this.deploymentWindowBlock = null;
      this.deploymentWindowOverrideCategory = null;
      this.deploymentWindowOverrideReference = '';
      this.deploymentWindowOverrideConfirmed = false;
      this.deploymentWindowOverrideError = '';
    },
    formatDeploymentWindowDate(value) {
      return new Date(value).toLocaleString();
    },
    isExecutionPreflightUnavailable(err) {
      const response = err?.response;
      return response?.status === 404
        && response?.data?.error === 'CAPABILITY_DENIED'
        && response?.data?.capability === 'execution_preflight';
    },
    adoptExecutionPreflight(plan, payload) {
      this.executionPreflight = plan;
      this.executionPreflightPayloadSignature = JSON.stringify(payload || this.buildPayload());
      this.preflightMessage = this.$t('executionPreflightChanged');
    },
    async submit() {
      if (this.errors.length) return;
      if (this.deploymentWindowBlock && !this.deploymentWindowOverrideReady) {
        this.deploymentWindowOverrideError = this.$t('deploymentWindowOverrideIncomplete');
        return;
      }
      const payload = this.buildPayload();
      const signature = JSON.stringify(payload);
      if (!this.executionPreflight || signature !== this.executionPreflightPayloadSignature) {
        this.preflightLoading = true;
        this.preflightMessage = null;
        try {
          this.executionPreflight = (await axios.post(`/api/project/${this.projectId}/workflows/${this.workflow.id}/preflight`, payload)).data;
          this.executionPreflightPayloadSignature = signature;
          if (this.hasDenial) this.preflightMessage = this.$t('executionPreflightDenied');
        } catch (err) {
          if (this.isExecutionPreflightUnavailable(err)) {
            this.$emit('start', { payload: this.buildStartPayload(payload), review: null });
            return;
          }
          this.preflightMessage = getErrorMessage(err);
        } finally {
          this.preflightLoading = false;
        }
        return;
      }
      if (this.hasDenial) return;
      this.$emit('start', {
        payload: this.buildStartPayload(payload),
        review: {
          fingerprint: this.executionPreflight.fingerprint,
          reviewToken: this.executionPreflight.review_token,
        },
      });
    },
  },
};
</script>
