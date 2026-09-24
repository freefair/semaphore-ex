<template>
  <section class="host-history pa-4" :aria-label="$t('hostsHistory', { host })">
    <div class="d-flex align-center mb-2">
      <v-icon class="mr-2">mdi-history</v-icon>
      <h3 class="text-h6">{{ $t('hostsHistory', { host }) }}</h3>
    </div>
    <p class="text-body-2 text--secondary">{{ $t('hostsHistoryCoverage') }}</p>
    <v-alert v-if="error" type="error" text>{{ error }}</v-alert>
    <div v-if="$vuetify.breakpoint.xsOnly">
      <v-progress-linear v-if="loading" indeterminate class="mb-3" />
      <p v-if="!loading && !items.length" class="text-body-2">{{ $t('hostsNoHistory') }}</p>
      <v-card v-for="item in items" :key="item.id" outlined class="mb-3 pa-3">
        <div class="d-flex align-center justify-space-between mb-2">
          <TaskLink :task-id="item.id" :label="`#${item.id}`" />
          <TaskStatus :status="item.status" />
        </div>
        <div class="text-body-2 font-weight-medium">{{ item.tpl_alias }}</div>
        <div class="text-body-2 mt-2">
          {{ $t('start') }}: {{ (item.start || item.created) | formatDate }}
        </div>
        <div class="text-body-2 mb-3">{{ $t('hostsTrigger') }}: {{ trigger(item) }}</div>
        <TaskLink :task-id="item.id" :label="$t('hostsTaskOutput')" />
      </v-card>
    </div>
    <v-data-table
      v-else
      :headers="headers"
      :items="items"
      :loading="loading"
      :items-per-page="-1"
      hide-default-footer
      :no-data-text="$t('hostsNoHistory')"
    >
      <template v-slot:item.id="{ item }">
        <TaskLink :task-id="item.id" :label="`#${item.id}`" />
        <div class="text-body-2">{{ item.tpl_alias }}</div>
      </template>
      <template v-slot:item.start="{ item }">{{
        (item.start || item.created) | formatDate
      }}</template>
      <template v-slot:item.status="{ item }"><TaskStatus :status="item.status" /></template>
      <template v-slot:item.trigger="{ item }">{{ trigger(item) }}</template>
      <template v-slot:item.output="{ item }">
        <TaskLink :task-id="item.id" :label="$t('hostsTaskOutput')" />
      </template>
    </v-data-table>
    <v-btn v-if="nextCursor" class="mt-3" outlined :loading="loading" @click="load(true)">
      {{ $t('hostsOlderTasks') }}
    </v-btn>
  </section>
</template>
<script>
import axios from 'axios';
import TaskLink from '@/components/TaskLink.vue';
import TaskStatus from '@/components/TaskStatus.vue';
import { getErrorMessage } from '@/lib/error';

export default {
  components: { TaskLink, TaskStatus },
  props: { projectId: Number, inventoryId: Number, host: String },
  data: () => ({
    items: [],
    nextCursor: null,
    loading: false,
    error: null,
    requestId: 0,
  }),
  computed: {
    headers() {
      return [
        { text: this.$t('hostsTask'), value: 'id' },
        { text: this.$t('start'), value: 'start' },
        { text: this.$t('status'), value: 'status' },
        { text: this.$t('hostsTrigger'), value: 'trigger' },
        { text: this.$t('hostsTaskOutput'), value: 'output' },
      ].map((header) => ({ ...header, sortable: false }));
    },
  },
  watch: {
    host() {
      this.load();
    },
    inventoryId() {
      this.load();
    },
  },
  mounted() {
    this.load();
  },
  beforeDestroy() {
    this.requestId += 1;
  },
  methods: {
    trigger(task) {
      if (task.workflow_run_id) return this.$t('hostsWorkflowTrigger', { id: task.workflow_run_id });
      if (task.integration_id) return this.$t('hostsIntegrationTrigger', { id: task.integration_id });
      if (task.schedule_id) return this.$t('hostsScheduleTrigger', { id: task.schedule_id });
      return task.user_name || this.$t('hostsUnknownTrigger');
    },
    async load(append = false) {
      const requestId = this.requestId + 1;
      this.requestId = requestId;
      this.loading = true;
      this.error = null;
      if (!append) {
        this.items = [];
        this.nextCursor = null;
      }
      try {
        const { data } = await axios.get(`/api/project/${this.projectId}/hosts/tasks`, {
          params: {
            inventory_id: this.inventoryId,
            host: this.host,
            before: append ? this.nextCursor : undefined,
          },
        });
        if (requestId !== this.requestId) return;
        this.items = append ? [...this.items, ...data.items] : data.items;
        this.nextCursor = data.next_cursor;
      } catch (error) {
        if (requestId === this.requestId) this.error = getErrorMessage(error);
      } finally {
        if (requestId === this.requestId) this.loading = false;
      }
    },
  },
};
</script>
