<template>
  <div class="host-browser">
    <p class="text-body-2 text--secondary">{{ $t('hostsMembershipDescription') }}</p>
    <div class="host-browser__filters">
      <v-text-field
        v-model="search"
        :label="$t('hostsSearch')"
        prepend-inner-icon="mdi-magnify"
        outlined
        dense
        clearable
        hide-details
        @input="searchChanged"
      />
      <v-select
        v-if="!inventoryId"
        v-model="filterInventoryId"
        :items="inventories"
        item-text="name"
        item-value="id"
        :label="$t('inventory')"
        outlined
        dense
        clearable
        hide-details
        @change="reload"
      />
      <v-switch
        v-if="!inventoryId"
        v-model="grouped"
        :label="$t('hostsGroupByInventory')"
        class="mt-0"
        hide-details
        inset
      />
      <v-btn icon :aria-label="$t('refresh')" :loading="loading" @click="reload">
        <v-icon>mdi-refresh</v-icon>
      </v-btn>
    </div>
    <v-alert v-if="error" type="error" text class="mt-4">{{ error }}</v-alert>
    <div v-if="$vuetify.breakpoint.xsOnly" class="mt-4">
      <v-progress-linear v-if="loading" indeterminate class="mb-3" />
      <p v-if="!loading && !rows.length" class="text-body-2">{{ $t('hostsNoHosts') }}</p>
      <div v-for="(item, index) in rows" :key="item.key">
        <div
          v-if="grouped && !inventoryId
            && (index === 0 || rows[index - 1].inventory_id !== item.inventory_id)"
          class="text-subtitle-1 mt-4 mb-2"
        >
          <router-link :to="`/project/${projectId}/inventories/${item.inventory_id}`">
            {{ item.inventory_name }}
          </router-link>
        </div>
        <v-card outlined class="mb-3">
          <div class="d-flex align-center px-3 pt-2">
            <v-btn text class="host-browser__host px-0" :aria-label="item.host"
                   @click="selectHost(item)">
              <v-icon left small>mdi-server</v-icon>{{ item.host }}
              <v-icon right small>
                {{ expanded[0]?.key === item.key ? 'mdi-chevron-up' : 'mdi-chevron-down' }}
              </v-icon>
            </v-btn>
          </div>
          <v-card-text class="pt-1 pb-3">
            <div v-if="!inventoryId && !grouped">
              <router-link :to="`/project/${projectId}/inventories/${item.inventory_id}`">
                {{ item.inventory_name }}
              </router-link>
            </div>
            <div>{{ $t('hostsGroups') }}: {{ (item.groups || []).join(', ') || '—' }}</div>
            <div class="text-caption mt-1">
              {{ $t('hostsResolvedAt') }}: {{ item.resolved_at | formatDate }}
            </div>
          </v-card-text>
          <template v-if="expanded[0]?.key === item.key">
            <v-divider />
            <HostTaskHistory :project-id="projectId" :inventory-id="item.inventory_id"
                             :host="item.host" />
          </template>
        </v-card>
      </div>
    </div>
    <v-data-table
      v-else
      class="mt-4"
      :headers="headers"
      :items="rows"
      :loading="loading"
      item-key="key"
      :items-per-page="-1"
      hide-default-footer
      show-expand
      single-expand
      :expanded.sync="expanded"
      :group-by="grouped && !inventoryId ? 'inventory_id' : null"
      :no-data-text="$t('hostsNoHosts')"
    >
      <template v-slot:group.header="{ group, items, toggle, isOpen }">
        <td :colspan="headers.length + 1">
          <v-btn icon small @click="toggle" :aria-label="$t('hostsToggleGroup')">
            <v-icon>{{ isOpen ? 'mdi-chevron-down' : 'mdi-chevron-right' }}</v-icon>
          </v-btn>
          <router-link :to="`/project/${projectId}/inventories/${group}`">
            {{ items[0].inventory_name }}
          </router-link>
        </td>
      </template>
      <template v-slot:item.host="{ item }">
        <v-btn text class="host-browser__host px-0" @click="selectHost(item)">{{
          item.host
        }}</v-btn>
      </template>
      <template v-slot:item.inventory_name="{ item }">
        <router-link :to="`/project/${projectId}/inventories/${item.inventory_id}`">
          {{ item.inventory_name }}
        </router-link>
      </template>
      <template v-slot:item.groups="{ item }">
        <span>{{ (item.groups || []).join(', ') || '—' }}</span>
      </template>
      <template v-slot:item.resolved_at="{ item }">{{ item.resolved_at | formatDate }}</template>
      <template v-slot:expanded-item="{ headers: tableHeaders, item }">
        <td :colspan="tableHeaders.length" class="pa-0">
          <HostTaskHistory
            :key="item.key"
            :project-id="projectId"
            :inventory-id="item.inventory_id"
            :host="item.host"
          />
        </td>
      </template>
    </v-data-table>
    <div class="d-flex justify-end align-center mt-3">
      <span class="text-body-2 text--secondary mr-3">{{
        $t('hostsDisplayed', { count: rows.length })
      }}</span>
      <v-btn
        :disabled="loading || cursors.length === 1"
        icon
        :aria-label="$t('hostsPreviousPage')"
        @click="previous"
        ><v-icon>mdi-chevron-left</v-icon></v-btn
      >
      <v-btn :disabled="loading || !nextCursor" icon :aria-label="$t('hostsNextPage')" @click="next"
        ><v-icon>mdi-chevron-right</v-icon></v-btn
      >
    </div>
  </div>
</template>
<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import HostTaskHistory from './HostTaskHistory.vue';

export default {
  components: { HostTaskHistory },
  props: { projectId: Number, inventoryId: Number, revision: Number },
  data: () => ({
    search: '',
    filterInventoryId: null,
    grouped: true,
    inventories: [],
    rows: [],
    expanded: [],
    loading: false,
    error: null,
    cursors: [null],
    nextCursor: null,
    requestId: 0,
    searchTimer: null,
  }),
  computed: {
    headers() {
      return [
        { text: this.$t('hostsHost'), value: 'host' },
        ...(!this.inventoryId ? [{ text: this.$t('inventory'), value: 'inventory_name' }] : []),
        { text: this.$t('hostsGroups'), value: 'groups' },
        { text: this.$t('hostsResolvedAt'), value: 'resolved_at' },
      ].map((header) => ({ ...header, sortable: false }));
    },
  },
  watch: {
    revision() {
      this.reload();
    },
    inventoryId() {
      this.reload();
    },
  },
  async mounted() {
    this.reload();
    if (!this.inventoryId) {
      try {
        this.inventories = (
          await axios.get(`/api/project/${this.projectId}/inventory`)
        ).data.filter((item) => ['file', 'static', 'static-yaml'].includes(item.type));
      } catch (error) {
        this.error = getErrorMessage(error);
      }
    }
  },
  beforeDestroy() {
    clearTimeout(this.searchTimer);
    this.requestId += 1;
  },
  methods: {
    selectHost(item) {
      this.expanded = this.expanded[0]?.key === item.key ? [] : [item];
    },
    searchChanged() {
      clearTimeout(this.searchTimer);
      this.requestId += 1;
      this.searchTimer = setTimeout(() => this.reload(), 250);
    },
    reload() {
      this.cursors = [null];
      this.load();
    },
    next() {
      this.cursors.push(this.nextCursor);
      this.load();
    },
    previous() {
      this.cursors.pop();
      this.load();
    },
    async load() {
      const requestId = this.requestId + 1;
      this.requestId = requestId;
      this.loading = true;
      this.error = null;
      try {
        const cursor = this.cursors[this.cursors.length - 1];
        const { data } = await axios.get(`/api/project/${this.projectId}/hosts`, {
          params: {
            inventory_id: this.inventoryId || this.filterInventoryId || undefined,
            search: this.search || undefined,
            after_inventory_id: cursor?.inventoryId,
            after_host: cursor?.host,
          },
        });
        if (requestId !== this.requestId) return;
        this.rows = data.items.map((item) => ({
          ...item,
          key: `${item.inventory_id}:${item.host}`,
        }));
        const expandedKeys = new Set(this.expanded.map((item) => item.key));
        this.expanded = this.rows.filter((row) => expandedKeys.has(row.key));
        this.nextCursor = data.next_host
          ? { inventoryId: data.next_inventory_id, host: data.next_host }
          : null;
      } catch (error) {
        if (requestId === this.requestId) {
          this.error = getErrorMessage(error);
          this.rows = [];
        }
      } finally {
        if (requestId === this.requestId) this.loading = false;
      }
    },
  },
};
</script>
<style scoped>
.host-browser ::v-deep .v-data-table .v-data-table__wrapper > table > tbody > tr > td {
  white-space: normal;
  overflow-wrap: anywhere;
}
.host-browser__filters {
  display: flex;
  gap: 16px;
  align-items: center;
  flex-wrap: wrap;
}
.host-browser__filters > .v-input {
  min-width: 180px;
  flex: 1 1 220px;
}
.host-browser__host {
  text-transform: none;
  letter-spacing: normal;
  white-space: normal;
}
</style>
