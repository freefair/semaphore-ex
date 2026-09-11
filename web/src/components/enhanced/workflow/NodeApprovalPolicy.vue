<template>
  <div>
    <slot />
  <v-select
    v-model="roleIdsModel"
    :items="workflowRoleOptions"
    item-value="value"
    item-text="text"
    :label="$t('workflowApprovalRoles')"
    :disabled="!canAdminister"
    multiple
    chips
    small-chips
    deletable-chips
    outlined
    dense
    hide-details="auto"
    class="mb-2"
    @change="$emit('policy-change')"
  />
  <v-select
    v-model="modeModel"
    :items="approvalRoleModeOptions"
    item-value="value"
    item-text="text"
    :label="$t('workflowApprovalRoleMode')"
    :disabled="!canAdminister"
    outlined
    dense
    hide-details="auto"
    class="mb-2"
    @change="$emit('policy-change')"
  />
  <v-text-field
    v-model.number="minimumApproversModel"
    type="number"
    min="1"
    :label="$t('workflowApprovalMinimumApprovers')"
    :disabled="!canAdminister"
    outlined
    dense
    hide-details="auto"
    class="mb-2"
    @change="$emit('policy-change')"
  />
  <v-select
    v-model="timeoutOutcomeModel"
    :items="approvalTimeoutOutcomeOptions"
    item-value="value"
    item-text="text"
    :label="$t('workflowApprovalTimeoutOutcome')"
    :disabled="!canManage"
    outlined
    dense
    hide-details="auto"
    class="mb-2"
    @change="$emit('edit')"
  />
  <v-switch
    v-model="initiatorSeparationModel"
    :label="$t('workflowApprovalSeparationOfDuties')"
    :disabled="!canAdminister"
    dense
    hide-details
    class="mt-0"
    @change="$emit('policy-change')"
  />
  </div>
</template>

<script>
export default {
  props: {
    policy: Object,
    timeoutOutcome: String,
    workflowRoleOptions: Array,
    approvalRoleModeOptions: Array,
    approvalTimeoutOutcomeOptions: Array,
    canManage: Boolean,
    canAdminister: Boolean,
  },
  computed: {
    roleIdsModel: {
      get() { return this.policy.role_ids; },
      set(value) { this.$emit('policy-field', 'role_ids', value); },
    },
    modeModel: {
      get() { return this.policy.mode; },
      set(value) { this.$emit('policy-field', 'mode', value); },
    },
    minimumApproversModel: {
      get() { return this.policy.minimum_distinct_approvers; },
      set(value) { this.$emit('policy-field', 'minimum_distinct_approvers', value); },
    },
    initiatorSeparationModel: {
      get() { return this.policy.initiator_separation; },
      set(value) { this.$emit('policy-field', 'initiator_separation', value); },
    },
    timeoutOutcomeModel: {
      get() { return this.timeoutOutcome; },
      set(value) { this.$emit('update:timeoutOutcome', value); },
    },
  },
};
</script>
