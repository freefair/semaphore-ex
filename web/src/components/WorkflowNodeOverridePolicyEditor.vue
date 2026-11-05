<template>
  <v-expansion-panels
    flat
    variant="accordion"
    class="WorkflowNodeOverridePolicyEditor mb-4"
    data-testid="workflow-node-override-policy"
  >
    <v-expansion-panel>
      <v-expansion-panel-title class="px-0 py-2">
        {{ $t('workflowNodeOverrides') }}
      </v-expansion-panel-title>
      <v-expansion-panel-text class="WorkflowNodeOverridePolicyEditor__content">
        <v-autocomplete
          v-if="canOverrideInventory || policy.inventory_ids.length"
          v-model="policy.inventory_ids"
          :items="inventories"
          item-value="id"
          item-title="name"
          :label="$t('workflowApprovedInventories')"
          :disabled="disabled || !canOverrideInventory"
          multiple
          chips

          density="compact"
          hide-details="auto"
          @update:model-value="emitValue"
        />
        <v-autocomplete
          v-model="policy.environment_ids"
          :items="environments"
          item-value="id"
          item-title="name"
          :label="$t('workflowApprovedEnvironments')"
          :disabled="disabled"
          multiple
          chips

          density="compact"
          hide-details="auto"
          @update:model-value="emitValue"
        />
        <v-select
          v-if="secretParameters.length || policy.credential_parameters.length"
          v-model="policy.credential_parameters"
          :items="secretParameters"
          item-value="name"
          item-title="name"
          :label="$t('workflowApprovedCredentialParameters')"
          :disabled="disabled"
          multiple
          chips

          density="compact"
          hide-details="auto"
          @update:model-value="emitValue"
        />
        <v-switch
          v-if="template && (template.allow_override_args_in_task || policy.allow_arguments)"
          v-model="policy.allow_arguments"
          :label="$t('workflowAllowArgumentsOverride')"
          :disabled="disabled || !template.allow_override_args_in_task"
          density="compact"
          hide-details
          @update:model-value="emitValue"
        />
        <v-switch
          v-if="template && (template.allow_override_branch_in_task || policy.allow_branch)"
          v-model="policy.allow_branch"
          :label="$t('workflowAllowBranchOverride')"
          :disabled="disabled || !template.allow_override_branch_in_task"
          density="compact"
          hide-details
          @update:model-value="emitValue"
        />
        <div class="text-body-small text--secondary mt-2">
          {{ $t('workflowNodeOverridesHint') }}
        </div>
      </v-expansion-panel-text>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

export default {
  emits: ['update:modelValue'],
  props: {
    modelValue: { type: Object, default: () => ({}) },
    projectId: { type: Number, required: true },
    template: { type: Object, default: null },
    parameters: { type: Array, default: () => [] },
    disabled: Boolean,
  },
  data() {
    return {
      policy: this.preparePolicy(this.modelValue),
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
    modelValue: {
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
      this.$emit('update:modelValue', JSON.parse(JSON.stringify(this.policy)));
    },
  },
};
</script>

<style scoped>
.WorkflowNodeOverridePolicyEditor__content {
  margin: 0 -16px;
}
</style>
