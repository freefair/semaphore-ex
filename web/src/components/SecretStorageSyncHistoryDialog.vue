<template>
  <v-dialog v-model="dialog" max-width="900" scrollable>
    <v-card>
      <v-card-title class="d-flex align-center">
        Synchronization history
        <v-spacer />
        <v-btn icon aria-label="Close synchronization history" @click="dialog = false">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>
      <v-card-subtitle v-if="storage">
        {{ storage.name }} · Secret values are never included
      </v-card-subtitle>
      <v-card-text>
        <v-progress-linear v-if="loading" indeterminate color="primary" />
        <v-alert v-else-if="error" type="error" text>
          {{ error }}
          <v-btn text color="primary" @click="open(storage)">Retry</v-btn>
        </v-alert>
        <v-alert v-else-if="history.length === 0" type="info" text>
          No synchronization has been attempted yet.
        </v-alert>
        <v-expansion-panels v-else accordion>
          <v-expansion-panel v-for="operation in history" :key="operation.id">
            <v-expansion-panel-header>
              <div class="sync-operation-summary">
                <v-chip small :color="statusColor(operation.status)" dark>
                  {{ operation.status }}
                </v-chip>
                <span>{{ formatTimestamp(operation.finished_at || operation.created_at) }}</span>
                <span>
                  {{ operation.changed_count }} changed · {{ operation.skipped_count }} skipped ·
                  {{ operation.conflict_count }} conflicts
                </span>
              </div>
            </v-expansion-panel-header>
            <v-expansion-panel-content>
              <v-alert v-if="operation.error_category" dense text type="warning">
                {{ formatValue(operation.error_category) }}
              </v-alert>
              <v-simple-table dense>
                <thead>
                  <tr>
                    <th>Status</th>
                    <th>Semaphore key</th>
                    <th>Remote reference</th>
                    <th>Version</th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="outcome in operation.outcomes"
                    :key="`${operation.id}-${outcome.mapping_id}`"
                  >
                    <td>{{ outcome.status }}</td>
                    <td>{{ keyName(outcome.access_key_id) }}</td>
                    <td><code>{{ outcome.mount }}/{{ outcome.path }}#{{ outcome.field }}</code></td>
                    <td>{{ outcome.remote_version || '—' }}</td>
                  </tr>
                </tbody>
              </v-simple-table>
              <v-btn
                v-if="operation.status === 'conflict'"
                class="mt-4"
                color="warning"
                :disabled="!canResolve || resolving"
                @click="$emit('resolve', { storageId: storage.id, operationId: operation.id })"
              >
                Confirm overwrite of observed versions
              </v-btn>
            </v-expansion-panel-content>
          </v-expansion-panel>
        </v-expansion-panels>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    projectId: Number,
    keys: { type: Array, default: () => [] },
    canResolve: Boolean,
    resolving: Boolean,
  },
  data() {
    return {
      dialog: false,
      loading: false,
      error: '',
      history: [],
      storage: null,
    };
  },
  methods: {
    async open(storage) {
      if (!storage) return;
      this.storage = storage;
      this.dialog = true;
      this.loading = true;
      this.error = '';
      try {
        this.history = (
          await axios.get(
            `/api/project/${this.projectId}/secret_storages/${storage.id}/sync/history?limit=25`,
          )
        ).data;
      } catch (error) {
        this.error = getErrorMessage(error);
      } finally {
        this.loading = false;
      }
    },
    keyName(accessKeyId) {
      return this.keys.find((key) => key.id === accessKeyId)?.name || `#${accessKeyId}`;
    },
    formatValue(value) {
      return (value || 'unknown').replace(/_/g, ' ');
    },
    formatTimestamp(value) {
      return value ? new Date(value).toLocaleString() : 'Never';
    },
    statusColor(status) {
      return {
        succeeded: 'success',
        conflict: 'warning',
        failed: 'error',
        running: 'info',
        pending: 'info',
      }[status] || 'grey';
    },
  },
};
</script>

<style scoped>
.sync-operation-summary {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
</style>
