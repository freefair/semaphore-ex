<template xmlns:v-slot="http://www.w3.org/1999/XSL/Transform">
  <div v-if="!isLoaded">
    <v-progress-linear
      indeterminate
      color="primary darken-2"
    ></v-progress-linear>
  </div>
  <div v-else>
    <v-dialog
      v-model="editViewsDialog"
      :max-width="400"
      persistent
      :transition="false"
    >
      <v-card>
        <v-card-title>
          {{ $t('editViews') }}
          <v-spacer></v-spacer>
          <v-btn icon @click="closeEditViewDialog()">
            <v-icon>mdi-close</v-icon>
          </v-btn>
        </v-card-title>
        <v-card-text>
          <EditViewsForm :project-id="projectId"/>
        </v-card-text>
      </v-card>
    </v-dialog>

    <EditTemplateDialog
      v-model="editDialog"
      :project-id="projectId"
      :item-app="itemApp"
      item-id="new"
      @save="loadItems()"
      :features="features"
    ></EditTemplateDialog>

    <NewTaskDialog
      v-model="newTaskDialog"
      @save="itemId = null"
      @close="itemId = null"
      :project-id="projectId"
      :template="template"
      :template-id="itemId"
    />

    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title>
        {{ $t('taskTemplates2') }}
      </v-toolbar-title>
      <v-spacer></v-spacer>

      <v-menu
        offset-y
      >
        <template v-slot:activator="{ on, attrs }">
          <v-btn
            v-bind="attrs"
            v-on="on"
            color="primary"
            class="mr-1 pr-2"
            v-if="can(USER_PERMISSIONS.manageProjectResources)"
            :disabled="!isAdmin && appsMixin.activeAppIds.length === 0"
          >
            {{ $t('newTemplate') }}
            <v-icon>mdi-chevron-down</v-icon>
          </v-btn>
        </template>
        <v-list>
          <v-list-item
            v-for="appID in appsMixin.activeAppIds"
            :key="appID"
            link
            @click="editItem('new'); itemApp = appID;"
          >
            <v-list-item-icon>
              <v-icon
                :color="getAppColor(appID)"
              >
                {{ getAppIcon(appID) }}
              </v-icon>
            </v-list-item-icon>
            <v-list-item-title>{{ getAppTitle(appID) }}</v-list-item-title>
          </v-list-item>

          <v-divider v-if="isAdmin && appsMixin.activeAppIds.length > 0"/>

          <v-list-item
            v-if="isAdmin"
            key="other"
            link
            to="/apps"
          >
            <v-list-item-icon>
              <v-icon>mdi-cogs</v-icon>
            </v-list-item-icon>
            <v-list-item-title>Applications</v-list-item-title>
          </v-list-item>
        </v-list>
      </v-menu>

      <v-btn icon @click="settingsSheet = true">
        <v-icon>mdi-cog</v-icon>
      </v-btn>
    </v-toolbar>

    <v-tabs show-arrows class="pl-4" v-model="viewTab">

      <v-tab
        v-for="(view) in views"
        :key="view.id"
        :to="getViewUrl(view.id)"
        :disabled="viewItemsLoading"
        :style="{
          'text-decoration': view.hidden ? 'line-through' : 'none',
        }"
      >{{ view.title }}
      </v-tab>

      <v-btn
        icon
        class="mt-2 ml-4"
        @click="editViewsDialog = true"
        v-if="can(USER_PERMISSIONS.manageProjectResources)"
      >
        <v-icon>mdi-pencil</v-icon>
      </v-btn>
    </v-tabs>

    <v-divider style="margin-top: -1px;"/>

    <TemplateSearchBar
      ref="templateSearch"
      :value.sync="templateSearchInput"
      :applied-search="appliedTemplateSearch"
      :loading="templateSearchLoading"
      :count="items.length"
      :error="templateSearchError"
      @input="queueTemplateSearch"
      @clear="clearTemplateSearch"
    />

    <v-data-table
      ref="templatesTable"
      hide-default-footer
      class="mt-4 templates-table"
      single-expand
      show-expand
      :headers="filteredHeaders"
      :items="items"
      :items-per-page="Number.MAX_VALUE"
      :page.sync="templateTablePage"
      :expanded.sync="openedItems"
      :style="{
        opacity: viewItemsLoading || templateSearchLoading ? 0.3 : 1,
      }"
    >
      <template v-slot:item.name="{ item }">
        <v-icon
          class="mr-3"
          small
        >
          {{ getAppIcon(item.app) }}
        </v-icon>

        <!--        <v-icon class="mr-3" small>-->
        <!--          {{ TEMPLATE_TYPE_ICONS[item.type] }}-->
        <!--        </v-icon>-->

        <router-link
          :to="viewId
              ? `/project/${projectId}/views/${viewId}/templates/${item.id}`
              : `/project/${projectId}/templates/${item.id}`"
        >
          <TemplateSearchHighlight :segments="highlightTemplateSearch(item.name)" />
        </router-link>
        <div
          v-if="templateSearchSecondaryMatch(item)"
          class="template-search-context ml-8"
        >
          {{ $t('templateSearchMatchedIn', { field: templateSearchSecondaryMatch(item).label }) }}:
          <TemplateSearchHighlight
            :segments="highlightTemplateSearch(templateSearchSecondaryMatch(item).value)"
          />
        </div>
      </template>

      <template v-slot:item.playbook="{ item }">
        <TemplateSearchHighlight :segments="highlightTemplateSearch(item.playbook)" />
      </template>

      <template v-slot:item.version="{ item }">
        <TaskLink
          v-if="item.last_task && item.last_task.tpl_type !== ''"
          :disabled="true"
          :status="item.last_task.status"

          :task-id="item.last_task.tpl_type === 'build'
              ? item.last_task.id
              : (item.last_task.build_task || {}).id"

          :label="item.last_task.tpl_type === 'build'
              ? item.last_task.version
              : (item.last_task.build_task || {}).version"

          :tooltip="item.last_task.tpl_type === 'build'
              ? item.last_task.message
              : (item.last_task.build_task || {}).message"
        />
        <div v-else>&mdash;</div>
      </template>

      <template v-slot:item.status="{ item }">
        <div class="mt-2 mb-2 d-flex" v-if="item.last_task != null">
          <TaskStatus :status="item.last_task.status"/>
        </div>
        <div v-else class="mt-3 mb-2 d-flex" style="color: gray;">{{ $t('notLaunched') }}</div>
      </template>

      <template v-slot:item.last_task="{ item }">
        <div class="mt-2 mb-2" v-if="item.last_task != null" style="line-height: 1">
          <TaskLink
            :task-id="item.last_task.id"
            :label="'#' + item.last_task.id"
            :tooltip="item.last_task.message"
          />
          <div style="color: gray; font-size: 14px;">
            {{ $t('by', {user_name: item.last_task.user_name}) }}
          </div>
        </div>
      </template>

      <template v-slot:item.inventory_id="{ item }">
        {{ (inventory.find((x) => x.id === item.inventory_id) || {name: '—'}).name }}
      </template>

      <template v-slot:item.environment_ids="{ item }">
        {{ formatEnvironmentNames(item) }}
      </template>

      <template v-slot:item.repository_id="{ item }">
        {{ repositories.find((x) => x.id === item.repository_id).name }}
      </template>

      <template v-slot:item.actions="{ item }">
        <v-btn-toggle dense :value-comparator="() => false">
          <v-btn
            v-if="canRun(item)"
            @click="createTask(item.id)"
          >
            <v-icon>mdi-play</v-icon>
          </v-btn>
        </v-btn-toggle>
      </template>

      <template v-slot:expanded-item="{ headers, item }">
        <td
          :colspan="headers.length"
          v-if="openedItems.some((template) => template.id === item.id)"
        >
          <TaskList
            style="border: 1px solid lightgray; border-radius: 6px; margin: 10px 0;"
            :template="item"
            :limit="5"
            :hide-footer="true"
          />
        </td>
      </template>

      <template v-slot:no-data>
        <div
          v-if="appliedTemplateSearch"
          class="template-search-empty py-8"
          data-testid="template-search-empty"
          aria-live="polite"
        >
          <div>{{ $t('templateSearchNoResults', { query: appliedTemplateSearch }) }}</div>
          <v-btn text color="primary" class="mt-2" @click="clearTemplateSearch">
            {{ $t('templateSearchClear') }}
          </v-btn>
        </div>
        <span v-else>{{ $t('templateSearchNoTemplates') }}</span>
      </template>
    </v-data-table>

    <TableSettingsSheet
      v-model="settingsSheet"
      table-name="project__template"
      :headers="headers"
      @change="onTableSettingsChange"
    />
  </div>
</template>
<style lang="scss">
@import '~vuetify/src/styles/settings/variables';

.templates-table .text-start:first-child {
  padding-right: 0 !important;
}

@import '../../components/enhanced/template-search';

@media #{map-get($display-breakpoints, 'sm-and-down')} {
  .templates-table .v-data-table__mobile-row:first-child {
    display: none !important;
  }
}
</style>
<script>
import TemplateSearchHighlight from '@/components/enhanced/TemplateSearchHighlight.vue';
import TemplateSearchBar from '@/components/enhanced/TemplateSearchBar.vue';
import { createEnhancedState, enhancedWatch } from '@/lib/enhanced/templates-state';

import enhancedMethods from '@/lib/enhanced/templates';

import ItemListPageBase from '@/components/ItemListPageBase';
import TaskLink from '@/components/TaskLink.vue';
import axios from 'axios';
import EditViewsForm from '@/components/EditViewsForm.vue';
import TableSettingsSheet from '@/components/TableSettingsSheet.vue';
import TaskList from '@/components/TaskList.vue';
import EventBus from '@/event-bus';
import TaskStatus from '@/components/TaskStatus.vue';
import socket from '@/socket';
import NewTaskDialog from '@/components/NewTaskDialog.vue';

import { TEMPLATE_TYPE_ACTION_TITLES, TEMPLATE_TYPE_ICONS } from '@/lib/constants';
import EditTemplateDialog from '@/components/EditTemplateDialog.vue';
import AppsMixin from '@/components/AppsMixin';

export default {
  components: {
    TemplateSearchHighlight,
    TemplateSearchBar,
    EditTemplateDialog,
    TableSettingsSheet,
    TaskStatus,
    TaskLink,
    TaskList,
    EditViewsForm,
    NewTaskDialog,
  },
  props: {
    features: Object,
  },
  mixins: [ItemListPageBase, AppsMixin],

  data() {
    const initialTemplateSearch = typeof this.$route.query.search === 'string'
      ? this.$route.query.search.trim().slice(0, 256)
      : '';
    return {
      TEMPLATE_TYPE_ICONS,
      TEMPLATE_TYPE_ACTION_TITLES,
      inventory: null,
      environment: null,
      repositories: null,
      newTaskDialog: null,
      settingsSheet: null,
      filteredHeaders: [],
      openedItems: [],
      views: null,
      editViewsDialog: null,
      viewItemsLoading: null,
      viewTab: null,
      apps: null,
      itemApp: '',
      templateSearchInput: initialTemplateSearch,
      appliedTemplateSearch: initialTemplateSearch,
      ...createEnhancedState(),
    };
  },

  computed: {

    viewId() {
      if (/^-?\d+$/.test(this.$route.params.viewId)) {
        return parseInt(this.$route.params.viewId, 10);
      }
      return this.$route.params.viewId;
    },

    template() {
      if (this.itemId == null || this.itemId === 'new') {
        return null;
      }
      return this.items.find((x) => x.id === this.itemId);
    },

    isLoaded() {
      return this.items
        && this.inventory
        && this.environment
        && this.repositories
        && this.views
        && this.isAppsLoaded;
    },
  },
  watch: {
    ...enhancedWatch,
    async viewId() {
      try {
        this.viewItemsLoading = true;
        await this.loadItems();
        if (this.viewId) {
          localStorage.setItem(`project${this.projectId}__lastVisitedViewId`, this.viewId);
        } else {
          localStorage.removeItem(`project${this.projectId}__lastVisitedViewId`);
        }
      } finally {
        this.viewItemsLoading = false;
      }
    },
  },

  async created() {
    this.socketListenerId = socket.addListener((data) => this.onWebsocketDataReceived(data));
    await this.loadData();
  },

  mounted() {
    window.addEventListener('keydown', this.onTemplateSearchShortcut);
  },

  beforeDestroy() {
    this.cancelQueuedTemplateSearch();
    this.cancelTemplateSearchRequest();
    window.removeEventListener('keydown', this.onTemplateSearchShortcut);
    socket.removeListener(this.socketListenerId);
  },

  methods: {
    ...enhancedMethods,

    async beforeLoadItems() {
      await this.loadViews();
      if (this.viewId == null) {
        let viewId = localStorage.getItem(`project${this.projectId}__lastVisitedViewId`);

        if (viewId == null) {
          viewId = this.views[0]?.id;
        }

        if (viewId != null
          && this.views.some((v) => v.id === parseInt(viewId, 10))) {
          const target = { path: `/project/${this.projectId}/views/${viewId}/templates` };
          if (this.appliedTemplateSearch) {
            target.query = { ...this.$route.query, search: this.appliedTemplateSearch };
          }
          await this.$router.push(target);
        }
      }
    },

    allowActions() {
      return true;
    },

    formatEnvironmentNames(item) {
      if (!Array.isArray(item.environment_ids) || item.environment_ids.length === 0) {
        return '—';
      }
      const names = item.environment_ids
        .map((id) => {
          const env = this.environment.find((x) => x.id === id);
          return env ? env.name : null;
        })
        .filter((n) => n != null);
      return names.length > 0 ? names.join(', ') : '—';
    },

    getViewUrl(viewId) {
      const path = viewId == null
        ? `/project/${this.projectId}/templates`
        : `/project/${this.projectId}/views/${viewId}/templates`;
      if (!this.appliedTemplateSearch) {
        return path;
      }
      return {
        path,
        query: { ...this.$route.query, search: this.appliedTemplateSearch },
      };
    },

    async loadViews() {
      this.views = (await axios({
        method: 'get',
        url: `/api/project/${this.projectId}/views`,
        responseType: 'json',
      })).data.filter((v) => !v.hidden || this.can(this.USER_PERMISSIONS.manageProjectResources));
      this.views.sort((v1, v2) => v1.position - v2.position);

      if (this.viewId != null && !this.views.some((v) => v.id === this.viewId)) {
        await this.$router.push({ path: `/project/${this.projectId}/templates` });
      }
    },

    async closeEditViewDialog() {
      this.editViewsDialog = false;
      await this.loadViews();
    },

    async onWebsocketDataReceived(data) {
      if (data.project_id !== this.projectId || data.type !== 'update') {
        return;
      }

      const template = (this.items || []).find((item) => item.id === data.template_id);

      if (template == null) {
        return;
      }

      if (data.task_id !== template.last_task_id) {
        // Set the id before awaiting so a burst of events for the same task
        // doesn't trigger duplicate requests while the first one is in flight.
        template.last_task_id = data.task_id;

        const lastTask = (await axios({
          method: 'get',
          url: `/api/project/${this.projectId}/tasks/${data.task_id}`,
          responseType: 'json',
        })).data;

        if (template.last_task) {
          Object.assign(template.last_task, lastTask);
        } else {
          template.last_task = lastTask;
        }
      }

      if (template.last_task) {
        Object.assign(template.last_task, {
          ...data,
          type: undefined,
        });
      }
    },

    showTaskLog(taskId) {
      EventBus.$emit('i-show-task', {
        taskId,
      });
    },

    canRun(item) {
      if (this.isAdmin) {
        return true;
      }

      const perm = this.USER_PERMISSIONS.runProjectTasks;

      if (item.permissions != null) {
        // eslint-disable-next-line no-bitwise
        return (item.permissions & perm) === perm;
      }

      // eslint-disable-next-line no-bitwise
      return (this.userPermissions & perm) === perm;
    },

    createTask(itemId) {
      this.itemId = itemId;
      this.newTaskDialog = true;
    },

    getHeaders() {
      return [
        {
          text: this.$i18n.t('name'),
          value: 'name',
        },
        {
          value: 'actions',
          sortable: false,
          width: '0%',
        },
        {
          text: this.$i18n.t('version'),
          value: 'version',
          sortable: false,
        },
        {
          text: this.$i18n.t('status'),
          value: 'status',
          sortable: false,
        },
        {
          text: this.$i18n.t('lastTask'),
          value: 'last_task',
          sortable: false,
        },
        {
          text: this.$i18n.t('playbook'),
          value: 'playbook',
          sortable: false,
        },
        {
          text: this.$i18n.t('inventory'),
          value: 'inventory_id',
          sortable: false,
        },
        {
          text: this.$i18n.t('environment'),
          value: 'environment_ids',
          sortable: false,
        },
        {
          text: this.$i18n.t('repository2'),
          value: 'repository_id',
          sortable: false,
        },
      ];
    },

    getItemsUrl() {
      return this.viewId == null
        ? `/api/project/${this.projectId}/templates`
        : `/api/project/${this.projectId}/views/${this.viewId}/templates`;
    },

    async loadData() {
      [
        this.inventory,
        this.environment,
        this.repositories,
      ] = await Promise.all([
        this.loadProjectResources('inventory'),
        this.loadProjectResources('environment'),
        this.loadProjectResources('repositories'),
      ]);
    },

    onTableSettingsChange({ headers }) {
      this.filteredHeaders = headers;
    },
  },
};
</script>
