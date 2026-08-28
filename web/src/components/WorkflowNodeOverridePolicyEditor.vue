<template>
  <v-expansion-panels
    flat
    accordion
    class="WorkflowNodeOverridePolicyEditor mb-4"
    data-testid="workflow-node-override-policy"
  >
    <v-expansion-panel>
      <v-expansion-panel-header class="px-0 py-2">
        {{ $t('workflowNodeOverrides') }}
      </v-expansion-panel-header>
      <v-expansion-panel-content class="WorkflowNodeOverridePolicyEditor__content">
        <v-autocomplete
          v-if="canOverrideInventory || policy.inventory_ids.length"
          v-model="policy.inventory_ids"
          :items="inventories"
          item-value="id"
          item-text="name"
          :label="$t('workflowApprovedInventories')"
          :disabled="disabled || !canOverrideInventory"
          multiple
          chips
          small-chips
          dense
          hide-details="auto"
          @change="emitValue"
        />
        <v-autocomplete
          v-model="policy.environment_ids"
          :items="environments"
          item-value="id"
          item-text="name"
          :label="$t('workflowApprovedEnvironments')"
          :disabled="disabled"
          multiple
          chips
          small-chips
          dense
          hide-details="auto"
          @change="emitValue"
        />
        <v-select
          v-if="secretParameters.length || policy.credential_parameters.length"
          v-model="policy.credential_parameters"
          :items="secretParameters"
          item-value="name"
          item-text="name"
          :label="$t('workflowApprovedCredentialParameters')"
          :disabled="disabled"
          multiple
          chips
          small-chips
          dense
          hide-details="auto"
          @change="emitValue"
        />
        <v-switch
          v-if="template && (template.allow_override_args_in_task || policy.allow_arguments)"
          v-model="policy.allow_arguments"
          :label="$t('workflowAllowArgumentsOverride')"
          :disabled="disabled || !template.allow_override_args_in_task"
          dense
          hide-details
          @change="emitValue"
        />
        <v-switch
          v-if="template && (template.allow_override_branch_in_task || policy.allow_branch)"
          v-model="policy.allow_branch"
          :label="$t('workflowAllowBranchOverride')"
          :disabled="disabled || !template.allow_override_branch_in_task"
          dense
          hide-details
          @change="emitValue"
        />
        <div class="text-caption text--secondary mt-2">
          {{ $t('workflowNodeOverridesHint') }}
        </div>
      </v-expansion-panel-content>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    value: { type: Object, default: () => ({}) },
    projectId: { type: Number, required: true },
    template: { type: Object, default: null },
    parameters: { type: Array, default: () => [] },
    disabled: Boolean,
  },
  data() {
    return {
      policy: this.preparePolicy(this.value),
      inventories: [],
      environments: [],
    };
  },
  computed: {
    canOverrideInventory() {
      return Boolean(this.template?.task_params?.allow_override_inventory);
    },
    secretParameters() {
      return this.parameters.filter((parameter) => parameter.type === 'secret_reference');
    },
  },
  watch: {
    value: {
      deep: true,
      handler(value) {
        const prepared = this.preparePolicy(value);
        if (JSON.stringify(prepared) !== JSON.stringify(this.policy)) this.policy = prepared;
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
    preparePolicy(value) {
      return {
        inventory_ids: [],
        environment_ids: [],
        credential_parameters: [],
        allow_arguments: false,
        allow_branch: false,
        ...(value || {}),
      };
    },
    emitValue() {
      this.$emit('input', JSON.parse(JSON.stringify(this.policy)));
    },
  },
};
</script>

<style scoped>
.WorkflowNodeOverridePolicyEditor__content {
  margin: 0 -16px;
}
</style>
