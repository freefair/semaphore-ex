<template xmlns:v-slot="http://www.w3.org/1999/XSL/Transform">
  <div v-if="items != null">
    <EditDialog
      v-model="editDialog"
      save-button-text="Save"
      :title="itemId === 'new' ? $t('newRole') : $t('editRole')"
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

    <YesNoDialog
      :title="$t('deleteRole')"
      :text="roleImpact ? $t('askDeleteAssignedRole', roleImpact) : ''"
      :yes-button-title="$t('deleteWithAssignments')"
      :max-width="520"
      v-model="forceDeleteDialog"
      @yes="deleteItem(itemId, true)"
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
      item-key="slug"
      class="mt-4"
      :footer-props="{ itemsPerPageOptions: [20] }"
    >
      <template v-slot:item.name="{ item }">
        <div>{{ item.name }}</div>
        <div class="text--secondary caption">{{ item.slug }}</div>
      </template>
      <template v-slot:item.scope="{ item }">
        <v-chip small label>{{ $t(roleScope(item)) }}</v-chip>
      </template>
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
        <div v-if="canManageRoles && !item.builtin" style="white-space: nowrap">
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
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/roles';

import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import YesNoDialog from '@/components/YesNoDialog.vue';
import ItemListPageBase from '@/components/ItemListPageBase';
import EditDialog from '@/components/EditDialog.vue';
import RoleForm from '@/components/EditRoleForm.vue';
import TeamMenu from '@/components/TeamMenu.vue';
import TemplatePermissionsChips from '@/components/TemplatePermissionsChips.vue';
import { roleScope } from '@/lib/roles';
import { USER_PERMISSIONS } from '@/lib/constants';

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
    return {
      roleImpact: null,
      forceDeleteDialog: false,
    };
  },

  computed: {
    ...enhancedComputed,
    IDFieldName() {
      return 'id';
    },

  },

  watch: {
    async projectId() {
      await this.loadItems();
    },
  },

  methods: {
    ...enhancedMethods,

    getHeaders() {
      const headers = [
        {
          text: this.$i18n.t('name'),
          value: 'name',
          width: '50%',
        },
        {
          text: this.$i18n.t('roleScope'),
          value: 'scope',
          sortable: false,
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

    roleScope,

    async deleteItem(itemId, force = false) {
      this.itemId = itemId;
      try {
        const item = this.items.find((x) => x[this.IDFieldName] === itemId);
        await axios({
          method: 'delete',
          url: `${this.getSingleItemUrl()}?revision=${encodeURIComponent(item.revision)}${force ? '&force=true' : ''}`,
          responseType: 'json',
        });
        EventBus.$emit(this.getEventName(), { action: 'delete', item });
        await this.loadItems();
      } catch (err) {
        const impact = err.response && err.response.status === 409
          && err.response.data && err.response.data.impact;
        if (impact && impact.mappings > 0 && !this.isAdmin) {
          EventBus.$emit('i-snackbar', { color: 'error', text: this.$i18n.t('roleUsedByMappings') });
          return;
        }
        if (impact && this.projectId && !this.can(USER_PERMISSIONS.manageProjectUsers)) {
          EventBus.$emit('i-snackbar', { color: 'error', text: this.$i18n.t('roleInUseNeedsManageUsers') });
          return;
        }
        if (impact) {
          this.roleImpact = impact;
          this.forceDeleteDialog = true;
          return;
        }
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      }
    },

    getItemsUrl() {
      return this.projectId ? `/api/project/${this.projectId}/roles` : '/api/roles';
    },

    getSingleItemUrl() {
      return this.projectId
        ? `/api/project/${this.projectId}/roles/${this.itemId}`
        : `/api/roles/${this.itemId}`;
    },

    getEventName() {
      return 'i-role';
    },
  },
};
</script>
