<template xmlns:v-slot="http://www.w3.org/1999/XSL/Transform">
  <div v-if="items != null">
    <EditDialog
      v-model="editDialog"
      save-button-text="Save"
      :title="$t('editRole')"
      @save="loadItems()"
    >
      <template v-slot:form="{ onSave, onError, needSave, needReset }">
        <RoleForm
          :project-id="projectId"
          :item-id="itemId"
          @save="onSave"
          @error="onError"
          :need-save="needSave"
          :need-reset="needReset"
          :is-admin="isAdmin"
        />
      </template>
    </EditDialog>

    <YesNoDialog
      :title="$t('deleteRole')"
      :text="$t('askDeleteRole')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <v-toolbar flat>
      <v-btn icon class="mr-4" @click="returnToProjects()">
        <v-icon>mdi-arrow-left</v-icon>
      </v-btn>
      <v-toolbar-title>{{ $t('Roles') }}</v-toolbar-title>
      <v-spacer></v-spacer>
      <v-btn
        v-if="canManageRoles"
        :disabled="!features.custom_roles_management"
        color="primary"
        @click="editItem('new')"
        >{{ $t('newRole') }}</v-btn
      >
    </v-toolbar>

    <TeamMenu
      v-if="projectId"
      :project-id="projectId"
      :system-info="systemInfo"
      :can-manage-roles="canManageRoles"
    />

    <v-divider style="margin-top: -1px" />

    <v-alert v-if="!features.custom_roles_management" text color="amber darken-3" class="PageAlert">
      Custom roles are not enabled.
    </v-alert>

    <v-alert
      v-else-if="!canManageRoles"
      text
      type="warning"
      class="PageAlert"
    >
      {{ projectId ? $t('projectRolePermissionDenied') : 'You cannot manage global roles.' }}
    </v-alert>

    <v-data-table
      :headers="headers"
      :items="items"
      class="mt-4"
      :footer-props="{ itemsPerPageOptions: [20] }"
    >
      <template v-slot:item.permissions="{ item }">
        <TemplatePermissionsChips class="py-1" :permissions="item.permissions" />
      </template>
      <template v-slot:item.global_permissions="{ item }">
        <TemplatePermissionsChips
          class="py-1"
          :permissions="item.global_permissions || 0"
          scope="global"
        />
      </template>
      <template v-slot:item.actions="{ item }">
        <div v-if="canManageRoles" style="white-space: nowrap">
          <v-btn icon class="mr-1" @click="askDeleteItem(item.id)">
            <v-icon>mdi-delete</v-icon>
          </v-btn>

          <v-btn icon class="mr-1" @click="editItem(item.id)">
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
        </div>
      </template>
    </v-data-table>
  </div>
</template>
<script>
import EventBus from '@/event-bus';
import YesNoDialog from '@/components/YesNoDialog.vue';
import ItemListPageBase from '@/components/ItemListPageBase';
import EditDialog from '@/components/EditDialog.vue';
import RoleForm from '@/components/EditRoleForm.vue';
import TeamMenu from '@/components/TeamMenu.vue';
import TemplatePermissionsChips from '@/components/TemplatePermissionsChips.vue';
import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

export default {
  mixins: [ItemListPageBase],

  props: {
    features: Object,
    projectId: Number,
    systemInfo: Object,
  },

  components: {
    TeamMenu,
    YesNoDialog,
    RoleForm,
    EditDialog,
    TemplatePermissionsChips,
  },

  data() {
    return {};
  },

  computed: {
    IDFieldName() {
      return 'id';
    },

    canManageRoles() {
      if (this.projectId) return this.can(USER_PERMISSIONS.manageProjectUsers);
      return hasGlobalPermission(this.systemInfo, GLOBAL_PERMISSIONS.manageRoles, this.isAdmin);
    },
  },

  watch: {
    async projectId() {
      await this.loadItems();
    },
  },

  methods: {
    allowActions() {
      return this.canManageRoles;
    },

    getHeaders() {
      const headers = [
        {
          text: this.$i18n.t('name'),
          value: 'name',
          width: '50%',
        },
        {
          text: this.projectId ? this.$i18n.t('permissions') : 'Global permissions',
          value: this.projectId ? 'permissions' : 'global_permissions',
        },
        {
          text: this.$i18n.t('actions'),
          value: 'actions',
          sortable: false,
        },
      ];
      if (!this.projectId) {
        headers.splice(2, 0, {
          text: 'Legacy project permissions',
          value: 'permissions',
        });
      }
      return headers;
    },

    async returnToProjects() {
      EventBus.$emit('i-open-last-project');
    },

    getItemsUrl() {
      return this.projectId ? `/api/project/${this.projectId}/roles` : '/api/roles';
    },

    getSingleItemUrl() {
      return this.projectId
        ? `/api/project/${this.projectId}/roles/${this.itemId}`
        : `/api/roles/${this.itemId}`;
    },

    getDeleteItemUrl(item) {
      return `${this.getSingleItemUrl()}?revision=${encodeURIComponent(item.revision)}`;
    },

    async loadItems() {
      if (!this.canManageRoles) {
        this.items = [];
        return;
      }
      this.items = await this.loadEndpoint(this.getItemsUrl());
    },

    getEventName() {
      return 'i-role';
    },
  },
};
</script>
