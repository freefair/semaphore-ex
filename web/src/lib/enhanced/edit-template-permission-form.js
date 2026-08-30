import { setTemplatePermissionEffect, templatePermissionEffect } from '@/lib/role-permissions';

const enhancedMethods = {
  async beforeLoadData() {
    await this.loadRoles();
  },
  permissionEffect(permission) {
    return templatePermissionEffect(this.item, permission);
  },
  permissionLabel(label) {
    return this.scope === 'default' ? this.$t(label) : label;
  },
  setPermissionEffect(permission, effect) {
    this.item = setTemplatePermissionEffect(this.item, permission, effect);
  },
};

export default enhancedMethods;
