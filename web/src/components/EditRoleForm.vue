<template>
  <v-form
    ref="form"
    lazy-validation
    v-model="formValid"
    v-if="item != null"
  >
    <v-alert
      :value="formError"
      color="error"
      class="pb-2"
    >{{ formError }}
    </v-alert>

    <v-text-field
      v-model="item.name"
      :label="$t('name')"
      :rules="[v => !!v || $t('name_required')]"
      outlined
      dense
      required
      :disabled="formSaving"
    ></v-text-field>

    <v-text-field
      v-if="!projectId"
      v-model="item.slug"
      :label="$t('slug')"
      :rules="[v => !!v || $t('slug_required'), v => this.validateSlug(v)]"
      outlined
      dense
      required
      :disabled="formSaving"
      :hint="$t('slugHint')"
    ></v-text-field>

    <v-subheader class="pl-0">
      {{ projectId ? $t('permissions') : 'Global permissions' }}
    </v-subheader>

    <template v-if="permissionCatalog.length > 0">
      <v-checkbox
        v-for="definition in permissionCatalog"
        :key="definition.id"
        class="mt-0"
        :input-value="hasPermission(definition.permission)"
        :label="definition.description"
        :disabled="formSaving"
        @change="setPermission(definition.permission, $event)"
      ></v-checkbox>
    </template>

    <template v-else>
      <v-alert text dense type="info">
        No assignable permissions are available for this edition.
      </v-alert>
    </template>

    <template v-if="!projectId">
      <v-subheader class="pl-0">Legacy project permissions</v-subheader>

      <v-checkbox
        class="mt-0"
        v-model="permissions.canRunProjectTasks"
        :label="$t('canRunProjectTasks')"
        :disabled="formSaving"
      ></v-checkbox>

      <v-checkbox
        class="mt-0"
        v-model="permissions.canUpdateProject"
        :label="$t('canUpdateProject')"
        :disabled="formSaving"
      ></v-checkbox>

      <v-checkbox
        class="mt-0"
        v-model="permissions.canManageProjectResources"
        :label="$t('canManageProjectResources')"
        :disabled="formSaving"
      ></v-checkbox>

      <v-checkbox
        class="mt-0"
        v-model="permissions.canManageProjectUsers"
        :label="$t('canManageProjectUsers')"
        :disabled="formSaving"
      ></v-checkbox>
    </template>

  </v-form>
</template>

<script>
import enhancedMethods from '@/lib/enhanced/edit-role-form';

import ItemFormBase from '@/components/ItemFormBase';

export default {
  mixins: [ItemFormBase],

  data() {
    return {
      permissionCatalog: [],
      permissions: {
        canRunProjectTasks: false,
        canUpdateProject: false,
        canManageProjectResources: false,
        canManageProjectUsers: false,
      },
    };
  },

  watch: {
    permissions: {
      handler(newPermissions) {
        if (this.projectId || !this.item) return;

        let permissionValue = 0;
        if (newPermissions.canRunProjectTasks) permissionValue |= 1;
        if (newPermissions.canUpdateProject) permissionValue |= 2;
        if (newPermissions.canManageProjectResources) permissionValue |= 4;
        if (newPermissions.canManageProjectUsers) permissionValue |= 8;

        this.item.permissions = permissionValue;
      },
      deep: true,
    },

    'item.permissions': {
      handler(newPermissions) {
        if (this.projectId || newPermissions === undefined || newPermissions === null) return;

        this.permissions.canRunProjectTasks = !!(newPermissions & 1);
        this.permissions.canUpdateProject = !!(newPermissions & 2);
        this.permissions.canManageProjectResources = !!(newPermissions & 4);
        this.permissions.canManageProjectUsers = !!(newPermissions & 8);
      },
      immediate: true,
    },
  },

  methods: {
    ...enhancedMethods,
    validateSlug(value) {
      if (!value) return true;
      return /^[a-z0-9_-]+$/.test(value) || this.$t('invalidSlugFormat');
    },

    getItemsUrl() {
      if (this.projectId) {
        return `/api/project/${this.projectId}/roles`;
      }
      return '/api/roles';
    },

    getSingleItemUrl() {
      if (this.projectId) {
        return `/api/project/${this.projectId}/roles/${this.itemId}`;
      }
      return `/api/roles/${this.itemId}`;
    },

    getNewItem() {
      const item = {
        name: '',
        permissions: 0,
      };
      if (!this.projectId) {
        item.slug = '';
        item.global_permissions = 0;
      }
      return item;
    },

    beforeSave() {
      if (this.projectId || !this.item) return;

      let permissionValue = 0;
      if (this.permissions.canRunProjectTasks) permissionValue |= 1;
      if (this.permissions.canUpdateProject) permissionValue |= 2;
      if (this.permissions.canManageProjectResources) permissionValue |= 4;
      if (this.permissions.canManageProjectUsers) permissionValue |= 8;
      this.item.permissions = permissionValue;
    },

    afterLoadData() {
      if (this.projectId || this.item.permissions === undefined) return;
      this.permissions.canRunProjectTasks = !!(this.item.permissions & 1);
      this.permissions.canUpdateProject = !!(this.item.permissions & 2);
      this.permissions.canManageProjectResources = !!(this.item.permissions & 4);
      this.permissions.canManageProjectUsers = !!(this.item.permissions & 8);
    },

    afterReset() {
      if (this.projectId) return;
      this.permissions = {
        canRunProjectTasks: false,
        canUpdateProject: false,
        canManageProjectResources: false,
        canManageProjectUsers: false,
      };
    },
  },
};
</script>

<style scoped>
.v-subheader {
  font-weight: 500;
  font-size: 14px;
}
</style>
