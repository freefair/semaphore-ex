<template>
  <div class="mb-5" data-testid="task-groups-editor">
    <v-alert v-if="error" type="error" dense>{{ error }}</v-alert>
    <v-autocomplete
      :value="value || []"
      @input="$emit('input', $event)"
      :items="options"
      item-value="id"
      item-text="label"
      :label="$t('taskGroupsTitle')"
      :hint="$t('taskGroupsSelectionHint')"
      :loading="loading"
      :disabled="disabled || loading || !!error"
      :rules="[v => (v || []).length <= 16 || $t('taskGroupsLimit')]"
      multiple chips small-chips deletable-chips outlined dense persistent-hint
      data-testid="task-groups-select"
    />
    <v-alert v-if="conflict" type="error" dense class="mt-3" data-testid="task-groups-conflict">
      {{ $t('taskGroupsRunnerConflict') }}
    </v-alert>
  </div>
</template>
<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    value: { type: Array, default: () => [] },
    projectId: { type: [Number, String], required: true },
    disabled: Boolean,
  },
  data: () => ({
    groups: [], loading: false, error: null, requestId: 0,
  }),
  computed: {
    options() {
      return this.groups.map((group) => ({
        ...group,
        label: group.project_id === Number(this.projectId) ? group.name
          : `${group.name} · ${this.$t('taskGroupsShared')}`,
      }));
    },
    conflict() {
      let allowed = null;
      this.groups.filter((group) => (this.value || []).includes(group.id)).forEach((group) => {
        if (!(group.runner_ids || []).length) return;
        allowed = allowed === null ? group.runner_ids
          : allowed.filter((id) => group.runner_ids.includes(id));
      });
      return allowed !== null && allowed.length === 0;
    },
  },
  watch: { projectId: { immediate: true, handler: 'load' } },
  methods: {
    async load() {
      this.requestId += 1;
      const requestId = this.requestId;
      this.loading = true;
      this.error = null;
      try {
        const response = await axios.get(`/api/project/${this.projectId}/task_groups`);
        if (requestId === this.requestId) this.groups = response.data;
      } catch (error) {
        if (requestId === this.requestId) this.error = getErrorMessage(error);
      } finally {
        if (requestId === this.requestId) this.loading = false;
      }
    },
  },
};
</script>
