<template>
  <v-form
    ref="form"
    v-model="formValid"
    v-if="item != null"
    data-testid="role-form"
    :data-state="formSaving ? 'loading' : (formError ? 'error' : 'ready')"
  >
    <v-alert
      :model-value="!!formError"
      color="error"
      class="pb-2"
      data-testid="role-error"
    >{{ formError }}
    </v-alert>

    <v-text-field
      v-model="item.name"
      :label="$t('name')"
      :rules="[v => !!v || $t('name_required')]"
      variant="outlined"
      density="compact"
      required
      :disabled="formSaving"
      data-testid="role-name"
    ></v-text-field>

    <v-text-field
      v-if="!projectId"
      v-model="item.slug"
      :label="$t('slug')"
      @update:model-value="slugTouched = true"
      :rules="slugRules"
      variant="outlined"
      density="compact"
      required
      :disabled="formSaving || !isNew"
      :hint="$t('slugHint')"
      data-testid="role-slug"
    ></v-text-field>

    <template v-if="projectId">
      <v-list-subheader class="pl-0">{{ $t('permissions') }}</v-list-subheader>
      <RolePermissionMatrix
        v-model="item.permission_ids"
        :catalog="permissionCatalog"
        :disabled="formSaving"
      />
    </template>

    <template v-else>
      <v-list-subheader class="pl-0">Global permissions</v-list-subheader>
      <v-checkbox
        v-for="definition in permissionCatalog"
        :key="definition.id"
        class="mt-0"
        :model-value="hasPermission(definition.permission)"
        :label="definition.description"
        :disabled="formSaving"
        @update:model-value="setPermission(definition.permission, $event)"
      ></v-checkbox>

      <v-list-subheader class="pl-0">{{ $t('permissions') }}</v-list-subheader>
      <RolePermissionMatrix
        v-model="item.permission_ids"
        :catalog="projectPermissionCatalog"
        :disabled="formSaving"
      />
    </template>

    <v-alert
      v-if="permissionCatalog.length === 0"
      variant="tonal"
      density="compact"
      type="info"
    >No assignable permissions are available.</v-alert>

  </v-form>
</template>

<script>
import enhancedMethods from '@/lib/enhanced/edit-role-form';

import ItemFormBase from '@/components/ItemFormBase';
import RolePermissionMatrix from '@/components/enhanced/RolePermissionMatrix.vue';
import { isValidSlug, slugFromName } from '@/lib/roles';

export default {
  components: { RolePermissionMatrix },
  mixins: [ItemFormBase],

  data() {
    return {
      permissionCatalog: [],
      projectPermissionCatalog: [],
      slugTouched: false,
    };
  },

  computed: {
    slugRules() {
      if (!this.isNew) return [];
      return [(v) => !!v || this.$t('slug_required'), (v) => this.validateSlug(v)];
    },
  },

  watch: {
    'item.name': function onNameChange(name) {
      if (!this.projectId && this.isNew && !this.slugTouched && this.item) {
        this.item.slug = slugFromName(name);
      }
    },
  },

  methods: {
    ...enhancedMethods,
    validateSlug(value) {
      if (!value) return true; // Required validation is handled separately

      if (!isValidSlug(value)) {
        return this.$t('invalidSlugFormat');
      }

      return true;
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
        permission_ids: [],
      };
      if (!this.projectId) {
        item.slug = '';
        item.global_permissions = 0;
      }
      return item;
    },

    afterReset() {
      this.slugTouched = false;
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
