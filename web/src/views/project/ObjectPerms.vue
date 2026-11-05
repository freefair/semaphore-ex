<template>
  <div
    v-if="items != null"
    data-testid="templatePerms-page"
    :data-state="items.length === 0 ? 'empty' : 'ready'"
  >
    <EditTemplatePermissionDialog
      v-model="editDialog"
      :project-id="projectId"
      :scope="scope"
      :object-id="object.id"
      :item-id="itemId"
      @save="loadItems()"
    />

    <YesNoDialog
      :title="$t('deleteTemplatePermission')"
      :text="$t('askDeleteTemplatePermission')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
      test-id="templatePermsDeleteDialog"
    />

    <v-alert
      v-if="!features.custom_roles_management"
      variant="tonal"
      color="amber darken-3"
      class="PageAlert"
    >
      Custom roles are not enabled.
    </v-alert>

    <v-btn
      v-if="allowActions()"
      :disabled="!features.custom_roles_management"
      color="primary"
      @click="editItem('new')"
      style="position: absolute; right: 16px"
      data-testid="templatePerms-new"
      >{{ $t('Add Role') }}
    </v-btn>

    <v-data-table
      :headers="headers"
      :items="items"
      item-value="role_slug"
      hide-default-footer
      class="mt-4 template-perms__table"
      :items-per-page="Number.MAX_VALUE"
      data-testid="templatePerms-table"
      :data-state="items.length === 0 ? 'empty' : 'ready'"
    >
      <template v-slot:item.role_slug="{ item }">
        <div class="d-flex align-center">
          <div>
            <div data-testid="templatePerms-rowName">{{ getRoleName(item.role_slug) }}</div>
            <div
              class="text-medium-emphasis text-body-small"
              data-testid="templatePerms-rowSlug"
            >{{ item.role_slug }}</div>
          </div>
          <v-chip
            v-if="getRole(item.role_slug)"
            size="x-small"
            label
            class="ml-2 template-perms__scope"
            data-testid="templatePerms-rowScope"
          >
            {{ $t(roleScope(getRole(item.role_slug))) }}
          </v-chip>
        </div>
      </template>

      <template v-slot:item.permissions="{ item }">
        <div v-if="scope === 'template'" class="py-1">
          <div class="caption text--secondary">Allow</div>
          <TemplatePermissionsChips
          :permissions="item.allowed_permissions || 0"
            scope="template"
          />
          <div class="caption text--secondary mt-1">Deny</div>
          <TemplatePermissionsChips
            :permissions="item.denied_permissions || 0"
            scope="template"
            effect="deny"
          />
        </div>
        <TemplatePermissionsChips v-else :permissions="item.permissions"
          :scope="scope"
          data-testid="templatePerms-rowPermissions"
        />
      </template>

      <template v-slot:item.actions="{ item }">
        <v-btn-group
          v-if="allowActions()" density="compact"
          data-testid="templatePerms-rowActions"
        >
          <v-btn @click="editItem(item.id)" data-testid="templatePerms-rowEdit">
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
          <v-btn @click="askDeleteItem(item.id)" data-testid="templatePerms-rowDelete">
            <v-icon>mdi-delete</v-icon>
          </v-btn>
        </v-btn-group>
      </template>
    </v-data-table>

    <v-card v-if="effectivePermissions" variant="outlined" class="mt-4 mb-4">
      <v-card-title class="subtitle-1">Your effective template permissions</v-card-title>
      <v-card-text class="pt-0">
        <div
          v-for="decision in effectivePermissions.decisions || []"
          :key="decision.permission"
          class="d-flex align-center mb-2"
        >
          <v-icon size="small" class="mr-2" :color="decision.allowed ? 'success' : 'grey'">
            {{ decision.allowed ? 'mdi-check-circle' : 'mdi-minus-circle-outline' }}
          </v-icon>
          <span>
            <strong>{{ permissionName(decision.permission) }}</strong>
            — {{ permissionProvenance(decision) }}
          </span>
        </div>
      </v-card-text>
    </v-card>
  </div>
</template>

<style lang="scss">
// Mobile rows: a flex row of Vuetify 2 (`.v-data-table__mobile-row`: space-between, the header
// 16 px from the value), not the two equal grid columns of Vuetify 4. The value takes the width its
// content needs at the right end of the row, so a single chip or the dash of no permissions ends
// at the right edge, and wrapped permission chips start right after the header.
.template-perms__table .v-data-table__tr--mobile > td {
  display: flex;
  justify-content: space-between;
  column-gap: 0;

  > .v-data-table__td-title {
    padding-right: 16px;
  }
}

// The scope chip: an x-small chip of Vuetify 2 is 16 px high (`.v-chip.v-size--x-small`), not 20,
// with the 20 px line of every chip (`.v-chip`), so its text sits as high as there.
.template-perms__scope.v-chip {
  --v-chip-height: 16px;
  line-height: 20px;
}
</style>

<script>
import enhancedMethods from '@/lib/enhanced/template-perms';

import ItemListPageBase from '@/components/ItemListPageBase';
import EditTemplatePermissionDialog from '@/components/EditTemplatePermissionDialog.vue';
import YesNoDialog from '@/components/YesNoDialog.vue';
import TemplatePermissionsChips from '@/components/TemplatePermissionsChips.vue';
import axios from 'axios';
import { USER_PERMISSIONS } from '@/lib/constants';
import { roleScope } from '@/lib/roles';

export default {
  components: {
    EditTemplatePermissionDialog,
    YesNoDialog,
    TemplatePermissionsChips,
  },
  mixins: [ItemListPageBase],

  // The tab of a template or of a workflow: the view passes the one it shows.
  props: {
    projectId: Number,
    template: Object,
    workflow: Object,
    repositories: Array,
    inventory: Array,
    environment: Array,
    features: Object,
    isAdmin: Boolean,
  },

  data() {
    return {
      USER_PERMISSIONS,
      availableRoles: [],
      effectivePermissions: null,
    };
  },

  computed: {
    scope() {
      return this.workflow ? 'workflow' : 'template';
    },

    templateId() { return this.template?.id; },

    object() {
      return this.workflow || this.template;
    },
  },

  methods: {
    ...enhancedMethods,

    async loadRoles() {
      try {
        const response = await axios.get(`/api/project/${this.projectId}/roles/all`);
        this.availableRoles = response.data || [];
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to load roles:', error);
      }
    },

    getRole(slug) {
      return this.availableRoles.find((role) => role.slug === slug);
    },

    getRoleName(slug) {
      const role = this.getRole(slug);
      return role ? role.name : slug;
    },

    roleScope,

    allowActions() {
      return this.workflow
        ? Boolean(this.workflow?.effective_access?.administer)
        : this.can(USER_PERMISSIONS.manageProjectResources);
    },

    getHeaders() {
      return [
        {
          title: this.$t('role'),
          key: 'role_slug',
          width: '25%',
        },
        {
          title: this.$t('permissions'),
          key: 'permissions',
          width: '65%',
        },
        {
          key: 'actions',
          sortable: false,
          width: '10%',
        },
      ];
    },

    getSingleItemUrl() {
      return `${this.getItemsUrl()}/${this.itemId}`;
    },

    getItemsUrl() {
      return `/api/project/${this.projectId}/${this.scope}s/${this.object.id}/perms`;
    },

    getEventName() {
      return `i-${this.scope}-perms`;
    },

  },
};
</script>
