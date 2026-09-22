<template>
  <div data-testid="task-group-membership">
    <v-chip v-for="group in groups" :key="group.key" small class="mr-1 mb-1">
      {{ group.label }}
    </v-chip>
    <div class="text-caption text--secondary">{{ $t('taskGroupsSnapshotHint') }}</div>
  </div>
</template>
<script>
import axios from 'axios';

export default {
  props: {
    value: { type: Array, default: () => [] },
    projectId: { type: [Number, String], required: true },
  },
  data: () => ({ catalog: [] }),
  computed: {
    groups() {
      return this.value.map((key) => {
        const id = Number(key.split('/')[1]);
        const group = this.catalog.find((entry) => entry.id === id);
        return { key, label: group?.name || this.$t('taskGroupsMember', { id }) };
      });
    },
  },
  async created() {
    try {
      this.catalog = (await axios.get(`/api/project/${this.projectId}/task_groups`)).data;
    } catch (error) {
      // Historical task IDs remain useful when a group was removed or its grant revoked.
      this.catalog = [];
    }
  },
};
</script>
