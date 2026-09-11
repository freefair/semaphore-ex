<template>
  <div class="pa-3">
    <v-text-field
      v-model="versionMessageModel"
      :label="$t('workflowVersionMessage')"
      :hint="$t('workflowVersionMessageHint')"
      :counter="512"
      :error-messages="versionMessageTooLong ? [$t('workflowVersionMessageTooLong')] : []"
      :disabled="!canManage"
      outlined
      dense
      @input="$emit('dirty')"
    />
    <slot name="start-version" />
    <v-text-field
      v-model.number="maxParallelTasksModel"
      type="number"
      min="1"
      max="32"
      :label="$t('workflowMaxParallelTasks')"
      :hint="$t('workflowMaxParallelTasksHint')"
      persistent-hint
      :disabled="!canManage"
      outlined
      dense
      @input="$emit('dirty')"
    />
    <v-select
      v-model="viewRoleIdsModel"
      :items="workflowRoleOptions"
      item-value="value"
      item-text="text"
      :label="$t('workflowViewRoles')"
      :hint="$t('workflowRoleRestrictionHint')"
      persistent-hint
      multiple
      chips
      small-chips
      deletable-chips
      :disabled="!canAdminister"
      outlined
      dense
      @change="$emit('dirty')"
    />
    <v-select
      v-model="startRoleIdsModel"
      :items="workflowRoleOptions"
      item-value="value"
      item-text="text"
      :label="$t('workflowStartRoles')"
      :hint="$t('workflowRoleRestrictionHint')"
      persistent-hint
      multiple
      chips
      small-chips
      deletable-chips
      :disabled="!canAdminister"
      outlined
      dense
      @change="$emit('dirty')"
    />
    <WorkflowParameterEditor
      v-model="parametersModel"
      :project-id="projectId"
      :disabled="!canManage"
      @input="$emit('dirty')"
    />
  </div>
</template>

<script>
import WorkflowParameterEditor from '@/components/WorkflowParameterEditor.vue';

export default {
  components: { WorkflowParameterEditor },
  props: {
    versionMessage: String,
    maxParallelTasks: [Number, String],
    viewRoleIds: Array,
    startRoleIds: Array,
    parameters: Array,
    projectId: Number,
    workflowRoleOptions: Array,
    versionMessageTooLong: Boolean,
    canManage: Boolean,
    canAdminister: Boolean,
  },
  computed: {
    versionMessageModel: {
      get() { return this.versionMessage; },
      set(value) { this.$emit('update:versionMessage', value); },
    },
    maxParallelTasksModel: {
      get() { return this.maxParallelTasks; },
      set(value) { this.$emit('update:maxParallelTasks', value); },
    },
    viewRoleIdsModel: {
      get() { return this.viewRoleIds; },
      set(value) { this.$emit('update:viewRoleIds', value); },
    },
    startRoleIdsModel: {
      get() { return this.startRoleIds; },
      set(value) { this.$emit('update:startRoleIds', value); },
    },
    parametersModel: {
      get() { return this.parameters; },
      set(value) { this.$emit('update:parameters', value); },
    },
  },
};
</script>
