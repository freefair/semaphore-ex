<template>
  <div>
    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer()" />
      <v-toolbar-title>{{ $t('taskGroupsTitle') }}</v-toolbar-title>
      <v-spacer />
      <v-btn v-if="can(USER_PERMISSIONS.createTaskGroups)" color="primary"
        @click="editItem('new')">{{ $t('taskGroupsCreate') }}</v-btn>
    </v-toolbar>
    <v-divider />
    <div class="pa-6">
      <p class="text--secondary">{{ $t('taskGroupsManagedDescription') }}</p>
      <v-alert v-if="error" type="error" dense>{{ error }}</v-alert>
      <v-data-table :headers="headers" :items="items || []" :loading="items === null">
        <template v-slot:item.ownership="{ item }">
          <v-chip small :outlined="item.project_id !== projectId">
            {{ $t(item.project_id === projectId ? 'taskGroupsOwned' : 'taskGroupsShared') }}
          </v-chip>
        </template>
        <template v-slot:item.runner_ids="{ item }">
          {{ (item.runner_ids || []).length || $t('taskGroupsUnrestricted') }}
        </template>
        <template v-slot:item.actions="{ item }">
            <v-btn v-if="canInProject(item.project_id, USER_PERMISSIONS.updateTaskGroups)" icon
              :aria-label="$t('edit')" @click="editItem(item.id)">
              <v-icon>mdi-pencil</v-icon>
            </v-btn>
            <v-btn v-if="canInProject(item.project_id, USER_PERMISSIONS.deleteTaskGroups)" icon
              :aria-label="$t('delete')" @click="askDeleteItem(item.id)">
              <v-icon>mdi-delete</v-icon>
            </v-btn>
        </template>
      </v-data-table>
    </div>
    <EditDialog v-model="editDialog"
      :title="$t(itemId === 'new' ? 'taskGroupsCreate' : 'taskGroupsEdit')"
      :save-button-text="$t(itemId === 'new' ? 'create' : 'save')"
      :max-width="640" @save="loadItems()">
      <template v-slot:form="{ onSave, onError, needSave, needReset }">
        <TaskGroupForm :key="`${itemProjectId}/${itemId}`"
          :project-id="itemProjectId" :item-id="itemId"
          :can-share="canInProject(itemProjectId, USER_PERMISSIONS.shareTaskGroups)"
          :need-save="needSave" :need-reset="needReset" @save="onSave" @error="onError" />
      </template>
    </EditDialog>
    <YesNoDialog v-model="deleteItemDialog" :title="$t('taskGroupsDelete')"
      :text="$t('taskGroupsDeleteHint')" @yes="deleteItem(itemId)" />
  </div>
</template>
<script>
import axios from 'axios';
import ItemListPageBase from '@/components/ItemListPageBase';
import TaskGroupForm from '@/components/enhanced/TaskGroupForm.vue';
import { getErrorMessage } from '@/lib/error';

export default {
  mixins: [ItemListPageBase],
  components: { TaskGroupForm },
  data: () => ({ error: null, ownerPermissions: {} }),
  computed: {
    itemProjectId() {
      if (this.itemId === 'new') return this.projectId;
      return (this.items || []).find((group) => group.id === this.itemId)?.project_id;
    },
  },
  methods: {
    canInProject(projectId, permission) {
      if (this.isAdmin) return true;
      if (projectId === this.projectId) return this.can(permission);
      return ((this.ownerPermissions[projectId] || 0) & permission) === permission;
    },
    allowActions() { return true; },
    getHeaders() {
      return [
        { text: this.$t('name'), value: 'name' },
        { text: this.$t('taskGroupsOwnership'), value: 'ownership' },
        { text: this.$t('taskGroupsConcurrency'), value: 'max_parallel_tasks' },
        { text: this.$t('taskGroupsRunners'), value: 'runner_ids' },
        { text: this.$t('actions'), value: 'actions', sortable: false },
      ];
    },
    getItemsUrl() { return `/api/project/${this.projectId}/task_groups`; },
    getSingleItemUrl() { return `/api/project/${this.itemProjectId}/task_groups/${this.itemId}`; },
    getEventName() { return 'i-task-groups'; },
    async loadItems() {
      this.ownerPermissions = {};
      try {
        this.items = (await axios.get(this.getItemsUrl())).data;
        await this.loadOwnerPermissions();
        this.error = null;
      } catch (error) {
        this.error = getErrorMessage(error);
        this.items = [];
      }
    },
    async loadOwnerPermissions() {
      const ownerIds = [...new Set(this.items.map((group) => group.project_id))]
        .filter((id) => id !== this.projectId);
      if (this.isAdmin || ownerIds.length === 0) return;
      const projects = (await axios.get('/api/projects')).data;
      const visibleOwners = ownerIds.filter((id) => projects.some((project) => project.id === id));
      const roles = await Promise.allSettled(visibleOwners.map(async (id) => {
        const role = (await axios.get(`/api/project/${id}/role`)).data;
        return [id, role.permissions];
      }));
      // An inaccessible or failed role lookup grants no editing controls.
      this.ownerPermissions = Object.fromEntries(roles
        .filter((role) => role.status === 'fulfilled').map((role) => role.value));
    },
    askDeleteItem(id) { this.itemId = id; this.deleteItemDialog = true; },
    async deleteItem(id) {
      const item = this.items.find((group) => group.id === id);
      try {
        await axios.delete(`/api/project/${item.project_id}/task_groups/${id}`, {
          data: { revision: item.revision },
        });
        await this.loadItems();
      } catch (error) { this.error = getErrorMessage(error); }
    },
  },
};
</script>
