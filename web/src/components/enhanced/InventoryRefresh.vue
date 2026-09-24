<template>
  <div>
    <v-btn color="primary" :loading="loading" @click="chooseContext">
      <v-icon left>mdi-refresh</v-icon>{{ $t('hostsRefresh') }}
    </v-btn>
    <v-dialog v-model="selecting" max-width="560">
      <v-card>
        <v-card-title>{{ $t('hostsRefresh') }}</v-card-title>
        <v-card-text>
          <p>{{ $t('hostsRefreshContext') }}</p>
          <v-alert v-if="error" type="error" text>{{ error }}</v-alert>
          <v-alert v-else-if="!templates.length" type="info" text>{{
            $t('hostsNoContext')
          }}</v-alert>
          <v-select
            v-else
            v-model="templateId"
            :items="templates"
            item-value="id"
            item-text="name"
            :label="$t('taskTemplates')"
            outlined
            dense
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="selecting = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="primary" text :disabled="!templateId" @click="openTask">
            {{ $t('hostsContinue') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
    <NewTaskDialog
      v-if="template"
      v-model="starting"
      :template="template"
      :project-id="projectId"
      :user-permissions="userPermissions"
      :is-admin="isAdmin"
      :source-task="sourceTask"
      @save="$emit('started', $event.item)"
    />
  </div>
</template>
<script>
import axios from 'axios';
import NewTaskDialog from '@/components/NewTaskDialog.vue';
import { getErrorMessage } from '@/lib/error';

export default {
  components: { NewTaskDialog },
  props: {
    projectId: Number,
    inventoryId: Number,
    userPermissions: Number,
    isAdmin: Boolean,
  },
  data: () => ({
    templates: [],
    templateId: null,
    template: null,
    selecting: false,
    starting: false,
    loading: false,
    error: null,
  }),
  computed: {
    sourceTask() {
      return {
        inventory_id: this.inventoryId,
        params: { inventory_refresh: true },
        message: this.$t('hostsRefresh'),
      };
    },
  },
  methods: {
    async chooseContext() {
      this.loading = true;
      this.error = null;
      this.templateId = null;
      try {
        const { data } = await axios.get(`/api/project/${this.projectId}/templates`);
        this.templates = data.filter(
          (template) => template.app === 'ansible'
            && (template.inventory_id === this.inventoryId
              || template.task_params?.allow_override_inventory),
        );
        if (this.templates.length === 1) this.templateId = this.templates[0].id;
      } catch (error) {
        this.error = getErrorMessage(error);
        this.templates = [];
      } finally {
        this.loading = false;
        this.selecting = true;
      }
    },
    async openTask() {
      try {
        this.template = (
          await axios.get(`/api/project/${this.projectId}/templates/${this.templateId}`)
        ).data;
        this.selecting = false;
        await this.$nextTick();
        this.starting = true;
      } catch (error) {
        this.error = getErrorMessage(error);
      }
    },
  },
};
</script>
