<template>
  <v-form v-if="item" ref="form" v-model="formValid" lazy-validation>
    <v-alert v-if="formError" type="error" dense outlined>{{ formError }}</v-alert>
    <v-select
      v-model="item.role_id"
      :items="roles"
      item-value="id"
      item-text="name"
      :label="$t('role')"
      :rules="[value => !!value || $t('role_required')]"
      :disabled="formSaving || !isNew"
      outlined
      dense
    />
    <v-checkbox
      v-for="permission in permissions"
      :key="permission.permission"
      :input-value="hasPermission(permission.permission)"
      :label="$t(permission.label)"
      :disabled="formSaving"
      @change="setPermission(permission.permission, $event)"
    />
  </v-form>
</template>

<script>
import axios from 'axios';
import ItemFormBase from '@/components/ItemFormBase';
import { ROLE_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';

export default {
  mixins: [ItemFormBase],
  props: { objectId: [Number, String] },
  data() {
    return { roles: [], permissions: ROLE_PERMISSIONS.workflow };
  },
  methods: {
    async beforeLoadData() {
      this.roles = (await axios.get(`/api/project/${this.projectId}/roles/all`)).data;
    },
    getItemsUrl() {
      return `/api/project/${this.projectId}/workflows/${this.objectId}/perms`;
    },
    getSingleItemUrl() { return `${this.getItemsUrl()}/${this.itemId}`; },
    getNewItem() { return { role_id: null, permissions: 0 }; },
    hasPermission(permission) { return (this.item.permissions & permission) === permission; },
    setPermission(permission, checked) {
      let mask = checked ? this.item.permissions | permission : this.item.permissions & ~permission;
      // Every object action requires that the object is visible.
      if (checked && permission !== USER_PERMISSIONS.readProject) {
        mask |= USER_PERMISSIONS.readProject;
      }
      if (!checked && permission === USER_PERMISSIONS.readProject) mask = 0;
      this.item.permissions = mask;
    },
  },
};
</script>
