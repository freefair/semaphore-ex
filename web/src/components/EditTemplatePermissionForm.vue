<template>
  <v-form
    ref="form"
    v-model="formValid"
    v-if="item != null"
    data-testid="templatePerm-form"
    :data-state="formSaving ? 'loading' : (formError ? 'error' : 'ready')"
  >
    <v-alert
      :model-value="!!formError"
      color="error"
      class="pb-2"
      data-testid="templatePerm-error"
    >{{ formError }}
    </v-alert>

    <v-select
      v-model="item.role_slug"
      :items="groupedRoles"
      item-value="slug"
      item-title="name"
      :label="$t('role')"
      :rules="[v => !!v || $t('role_required')]"
      required
      variant="outlined"
      density="compact"
      :disabled="formSaving"
      :menu-props="{ location: 'top', origin: 'top', maxHeight: 304 }"
      :list-props="{ class: 'template-perm-form__roles' }"
      data-testid="templatePerm-roleSlug"
    >
      <template v-slot:item="{ item: role, props }">
        <v-list-item v-bind="props" :title="undefined">
          <div :data-testid="`templatePerm-roleSlug-${role.slug}`">
            <v-list-item-title>{{ role.name }}</v-list-item-title>
            <v-list-item-subtitle>{{ role.slug }}</v-list-item-subtitle>
          </div>
        </v-list-item>
      </template>
    </v-select>

    <v-list-subheader
      class="pl-0"
      data-testid="templatePerm-permissionsTitle"
    >Templatepermissions</v-list-subheader>

    <v-select
      v-for="p in ROLE_PERMISSIONS[scope]"
      :key="p.permission"
      class="mt-0"

      :label="permissionLabel(p.label)"
      :items="permissionEffects"
      item-title="text"
      :model-value="permissionEffect(p.permission)"
      :disabled="formSaving "
      variant="outlined"
      density="compact"
      @update:model-value="setPermissionEffect (p.permission , $event)"
      :data-testid="`templatePerm-permission-${p.permission}`"
    ></v-select>

  </v-form>
</template>

<script>
import enhancedMethods from '@/lib/enhanced/edit-template-permission-form';

import ItemFormBase from '@/components/ItemFormBase';
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import { ROLE_PERMISSIONS } from '@/lib/constants';
import groupRolesForSelect from '@/lib/roles';

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

  computed: {
    groupedRoles() {
      return groupRolesForSelect(this.availableRoles, (key) => this.$t(key));
    },
  },

  methods: {
    ...enhancedMethods,

    async loadRoles() {
      try {
        const response = await axios.get(`/api/project/${this.projectId}/roles/all`);
        this.availableRoles = response.data || [];
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
        permissions: 0,
        allowed_permissions: 0,
        denied_permissions: 0,
      };
    },

    beforeSave() {
      if (this.item) {
        // Explicit allow/deny masks supersede the legacy compatibility projection.
        this.item.permissions = 0;
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
.v-list-subheader {
  font-weight: 500;
  font-size: 14px;
}
</style>

<style lang="scss">
// The role list of the dense select is a dense list of Vuetify 2 (`.v-list--dense`): headers
// 40 px high, 12 px, 8 px from the edge; two-line items 50 px (content padding 8 px, a title of
// 16 px on a 16 px line — App.vue of Vuetify 2 kept the dense title at 1rem and normal weight —,
// 2 px, then a subtitle of 13 px, weight 500, in the secondary text colour). The menu is at most
// 304 px high (`menu-props` of the Vuetify 2 `v-select`; Vuetify 4: 310), opening over the field
// as the VSelect defaults in plugins/vuetify.js say (a `menu-props` prop replaces them).
.template-perm-form__roles {
  > .v-list-subheader {
    min-height: 40px;
    padding-inline: 8px;
    font-size: .75rem;
  }

  > .v-list-item .v-list-item__content {
    padding-block: 8px;
  }

  > .v-list-item .v-list-item-title {
    line-height: 1rem;

    &:not(:last-child) {
      margin-bottom: 2px;
    }
  }

  > .v-list-item .v-list-item-subtitle {
    font-size: .8125rem;
    font-weight: 500;
    line-height: 1rem;
    opacity: 1;
  }

  > .v-list-item.v-theme--light .v-list-item-subtitle {
    color: rgba(0, 0, 0, .6);
  }

  > .v-list-item.v-theme--dark .v-list-item-subtitle {
    color: rgba(255, 255, 255, .7);
  }
}
</style>
