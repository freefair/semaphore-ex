import { GLOBAL_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

export const enhancedComputed = {
  canManageUsers() {
    return hasGlobalPermission(this.systemInfo, GLOBAL_PERMISSIONS.manageUsers, this.isAdmin);
  },
  canManageGlobalRoles() {
    return hasGlobalPermission(this.systemInfo, GLOBAL_PERMISSIONS.manageRoles, this.isAdmin);
  },
};

export const enhancedMethods = {
  allowActions() {
    return this.canManageUsers;
  },
  refreshHeaders() {
    this.headers = this.getHeaders().filter(
      (header) => this.canManageUsers || header.value !== 'actions',
    );
  },
  async loadItems() {
    if (!this.canManageUsers) {
      this.items = [];
      return;
    }
    this.items = await this.loadEndpoint(this.getItemsUrl());
  },
};
