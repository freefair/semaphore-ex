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

    <v-select
      v-model="item.role_slug"
      :items="availableRoles"
      item-value="slug"
      item-text="name"
      :label="$t('role')"
      :rules="[v => !!v || $t('role_required')]"
      required
      outlined
      dense
      :disabled="formSaving"
    >
      <template v-slot:item="{ item: role }">
        <v-list-item-content>
          <v-list-item-title>{{ role.name }}</v-list-item-title>
          <v-list-item-subtitle>{{ role.slug }}</v-list-item-subtitle>
        </v-list-item-content>
      </template>
    </v-select>

    <v-subheader class="pl-0">Template permissions</v-subheader>

    <v-select
      v-for="p in ROLE_PERMISSIONS[scope]"
      :key="p.permission"
      class="mt-0"
      :label="permissionLabel(p.label)"
      :items="permissionEffects"
      :value="permissionEffect(p.permission)"
      :disabled="formSaving"
      outlined
      dense
      @change="setPermissionEffect(p.permission, $event)"
    ></v-select>

  </v-form>
</template>

<script>
import enhancedMethods from '@/lib/enhanced/edit-template-permission-form';

import ItemFormBase from '@/components/ItemFormBase';
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import { ROLE_PERMISSIONS, USER_ROLES } from '@/lib/constants';

export default {
  mixins: [ItemFormBase],

  props: {
    templateId: [Number, String],
    scope: {
      type: String,
      default: 'default',
    },
  },

  data() {
    return {
      ROLE_PERMISSIONS,
      availableRoles: [],
      permissionEffects: [
        { text: 'Inherit from project role', value: 'inherit' },
        { text: 'Allow', value: 'allow' },
        { text: 'Deny', value: 'deny' },
      ],
    };
  },

  methods: {
    ...enhancedMethods,

    async loadRoles() {
      try {
        const response = await axios.get(`/api/project/${this.projectId}/roles/all`);
        this.availableRoles = [...USER_ROLES, ...(response.data || [])];
      } catch (error) {
        this.formError = getErrorMessage(error);
      }
    },

    getItemsUrl() {
      return `/api/project/${this.projectId}/templates/${this.templateId}/perms`;
    },

    getSingleItemUrl() {
      return `/api/project/${this.projectId}/templates/${this.templateId}/perms/${this.itemId}`;
    },

    getNewItem() {
      return {
        role_slug: null,
        template_id: parseInt(this.templateId, 10),
        project_id: this.projectId,
        permissions: 0,
        allowed_permissions: 0,
        denied_permissions: 0,
      };
    },

    beforeSave() {
      if (this.item) {
        this.item.template_id = parseInt(this.templateId, 10);
        this.item.project_id = this.projectId;
      }
    },

    afterLoadData() {
      if (!this.item) return;
      this.item.allowed_permissions = this.item.allowed_permissions || 0;
      this.item.denied_permissions = this.item.denied_permissions || 0;
    },

    afterReset() {
      this.formError = null;
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
