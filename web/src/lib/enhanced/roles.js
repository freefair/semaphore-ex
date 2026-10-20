import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

export const enhancedComputed = {
  canManageRoles() {
    if (this.projectId) return this.can(USER_PERMISSIONS.manageProjectUsers);
    return hasGlobalPermission(
      this.systemInfo || this.user,
      GLOBAL_PERMISSIONS.manageRoles,
      this.isAdmin,
    );
  },
};

export const enhancedMethods = {
  allowActions() {
    // Mixin data initializes table headers before Vue installs computed getters.
    return enhancedComputed.canManageRoles.call(this);
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
};
