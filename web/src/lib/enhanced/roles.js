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
    const catalogUrl = this.projectId
      ? `/api/project/${this.projectId}/roles/permissions`
      : '/api/roles/project-permissions';
    [this.items, this.projectPermissionCatalog] = await Promise.all([
      this.loadEndpoint(this.getItemsUrl()),
      // The summary falls back to raw IDs when the catalog is unavailable.
      this.loadEndpoint(catalogUrl).catch(() => []),
    ]);
  },
};
