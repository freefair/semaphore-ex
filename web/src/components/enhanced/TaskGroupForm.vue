<template>
  <v-form v-if="item" ref="form" v-model="formValid" class="pt-4">
    <v-alert v-if="formError" dense type="error">{{ formError }}</v-alert>
    <v-text-field v-model="item.name" :label="$t('name')" outlined dense
      :rules="[v => !!v?.trim() || $t('name_required')]" :disabled="formSaving" />
    <v-textarea v-model="item.description" :label="$t('description')" outlined dense rows="2"
      :disabled="formSaving" />
    <v-text-field v-model.number="item.max_parallel_tasks" type="number" min="1" max="1000"
      :label="$t('taskGroupsConcurrency')" :hint="$t('taskGroupsConcurrencyHint')"
      :rules="[concurrencyRule]"
      outlined dense persistent-hint class="mb-4" :disabled="formSaving" />
    <v-autocomplete v-model="item.runner_ids" :items="runners" item-value="id" item-text="name"
      :label="$t('taskGroupsRunners')" :hint="$t('taskGroupsRunnersHint')"
      multiple chips small-chips deletable-chips outlined dense persistent-hint class="mb-4"
      :disabled="formSaving" />
    <v-autocomplete v-model="item.shared_project_ids" :items="projects"
      item-value="id" item-text="name"
      :label="$t('taskGroupsProjectGrants')" :hint="$t('taskGroupsProjectGrantsHint')"
      multiple chips small-chips deletable-chips outlined dense persistent-hint
      :disabled="formSaving || !canShare" />
  </v-form>
</template>
<script>
import axios from 'axios';
import ItemFormBase from '@/components/ItemFormBase';

export default {
  mixins: [ItemFormBase],
  props: { canShare: Boolean },
  data: () => ({ runners: [], projects: [] }),
  methods: {
    concurrencyRule(value) {
      return (Number.isInteger(value) && value >= 1 && value <= 1000)
        || this.$t('taskGroupsConcurrencyInvalid');
    },
    getItemsUrl() { return `/api/project/${this.projectId}/task_groups`; },
    getSingleItemUrl() { return `${this.getItemsUrl()}/${this.itemId}`; },
    getNewItem() {
      return {
        name: '', description: '', max_parallel_tasks: 1, runner_ids: [], shared_project_ids: [],
      };
    },
    async afterLoadData() {
      const [runners, projects] = await Promise.all([
        axios.get(`${this.getItemsUrl()}/runners`), axios.get('/api/projects'),
      ]);
      this.runners = runners.data;
      this.projects = projects.data.filter((project) => project.id !== Number(this.projectId));
      this.item.runner_ids = this.item.runner_ids || [];
      this.item.shared_project_ids = this.item.shared_project_ids || [];
    },
    async beforeSave() {
      this.item.name = this.item.name.trim();
      this.item.runner_ids = [...this.item.runner_ids].sort((a, b) => a - b);
      this.item.shared_project_ids = [...this.item.shared_project_ids].sort((a, b) => a - b);
    },
  },
};
</script>
