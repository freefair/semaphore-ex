<template>
  <div>
    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer" />
      <v-toolbar-title>
        <router-link :to="`/project/${projectId}/inventory`">{{ $t('inventory') }}</router-link>
        <v-icon small class="mx-2">mdi-chevron-right</v-icon>
        {{ inventory ? inventory.name : $t('hostsInventoryDetails') }}
      </v-toolbar-title>
    </v-toolbar>
    <v-divider />
    <div class="pa-4">
      <v-alert v-if="error" type="error" text>{{ error }}</v-alert>
      <v-skeleton-loader v-if="loading" type="article" />
      <template v-else-if="inventory">
        <v-card outlined class="mb-4">
          <v-card-text class="d-flex align-center flex-wrap inventory-details__summary">
            <div class="flex-grow-1">
              <h2 class="text-h6 text--primary">{{ inventory.name }}</h2>
              <div>
                {{ $t('type') }}: <code>{{ inventory.type }}</code>
              </div>
              <div v-if="inventory.type === 'file'" class="mt-1">
                {{ $t('path') }}: <code>{{ inventory.inventory }}</code>
              </div>
            </div>
            <InventoryRefresh
              v-if="ansible && canRun"
              :project-id="projectId"
              :inventory-id="inventoryId"
              :user-permissions="userPermissions"
              :is-admin="isAdmin"
              @started="refreshStarted"
            />
          </v-card-text>
        </v-card>
        <v-alert v-if="!ansible" type="info" text>{{ $t('hostsAnsibleOnly') }}</v-alert>
        <template v-else>
          <v-alert v-if="refreshTask" type="info" text>
            {{ $t('hostsRefreshStarted') }}
            <TaskLink :task-id="refreshTask" :label="`#${refreshTask}`" />
          </v-alert>
          <v-alert v-if="latest && latest.state === 'error'" type="warning" text>
            {{ $t('hostsRefreshFailed') }}
            <TaskLink :task-id="latest.task_id" :label="`#${latest.task_id}`" />
          </v-alert>
          <p v-else-if="!latest" class="text-body-2">{{ $t('hostsNotResolved') }}</p>
          <p v-else-if="latest.state === 'collecting'" class="text-body-2">
            {{ $t('hostsCollecting') }}
          </p>
          <p v-else class="text-body-2 text--secondary">
            {{ $t('hostsResolvedAt') }}: {{ latest.created | formatDate }} ·
            {{ $t('hostsSnapshotCount', { count: latest.host_count }) }} ·
            <TaskLink :task-id="latest.task_id" :label="`#${latest.task_id}`" />
          </p>
          <HostBrowser
            :key="`${projectId}:${inventoryId}`"
            :project-id="projectId"
            :inventory-id="inventoryId"
            :revision="revision"
          />
        </template>
      </template>
    </div>
  </div>
</template>
<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import socket from '@/socket';
import { USER_PERMISSIONS } from '@/lib/constants';
import { getErrorMessage } from '@/lib/error';
import TaskLink from '@/components/TaskLink.vue';
import HostBrowser from '@/components/enhanced/HostBrowser.vue';
import InventoryRefresh from '@/components/enhanced/InventoryRefresh.vue';

export default {
  components: { TaskLink, HostBrowser, InventoryRefresh },
  props: { projectId: Number, userPermissions: Number, isAdmin: Boolean },
  data: () => ({
    inventory: null,
    latest: null,
    loading: false,
    error: null,
    revision: 0,
    refreshTask: null,
    requestId: 0,
    snapshotRequestId: 0,
  }),
  computed: {
    inventoryId() {
      return Number(this.$route.params.inventoryId);
    },
    ansible() {
      return ['static', 'static-yaml', 'file'].includes(this.inventory?.type);
    },
    canRun() {
      // eslint-disable-next-line no-bitwise
      return this.isAdmin || (this.userPermissions & USER_PERMISSIONS.runProjectTasks) !== 0;
    },
  },
  watch: {
    inventoryId() {
      this.load();
    },
    projectId() {
      this.load();
    },
  },
  created() {
    this.socketListener = socket.addListener((event) => {
      if (
        event.type === 'update'
        && event.project_id === this.projectId
        && ['success', 'error', 'stopped'].includes(event.status)
      ) this.loadSnapshots();
    });
    this.load();
  },
  beforeDestroy() {
    socket.removeListener(this.socketListener);
    this.requestId += 1;
    this.snapshotRequestId += 1;
  },
  methods: {
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },
    refreshStarted(task) {
      this.refreshTask = task.id;
      this.loadSnapshots();
    },
    async load() {
      const requestId = this.requestId + 1;
      this.requestId = requestId;
      this.loading = true;
      this.error = null;
      this.inventory = null;
      this.latest = null;
      this.refreshTask = null;
      try {
        const { data } = await axios.get(
          `/api/project/${this.projectId}/inventory/${this.inventoryId}`,
        );
        if (requestId !== this.requestId) return;
        this.inventory = data;
        if (this.ansible) await this.loadSnapshots();
      } catch (error) {
        if (requestId === this.requestId) this.error = getErrorMessage(error);
      } finally {
        if (requestId === this.requestId) this.loading = false;
      }
    },
    async loadSnapshots() {
      const inventoryId = this.inventoryId;
      const projectId = this.projectId;
      const requestId = this.snapshotRequestId + 1;
      this.snapshotRequestId = requestId;
      const isCurrent = () => requestId === this.snapshotRequestId
        && inventoryId === this.inventoryId && projectId === this.projectId;
      try {
        const { data } = await axios.get(
          `/api/project/${projectId}/inventory/${inventoryId}/hosts/snapshots`,
        );
        if (!isCurrent()) return;
        this.latest = data[0] || null;
        if (this.latest?.task_id === this.refreshTask && this.latest.state !== 'collecting') {
          this.refreshTask = null;
        }
        this.revision += 1;
      } catch (error) {
        if (isCurrent()) this.error = getErrorMessage(error);
      }
    },
  },
};
</script>
<style scoped>
.inventory-details__summary {
  gap: 16px;
}
</style>
