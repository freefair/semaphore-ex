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
    const [items, catalog, all] = await Promise.all([
      this.loadEndpoint(this.getItemsUrl()),
      // The summary falls back to raw IDs when the catalog is unavailable.
      this.loadEndpoint(catalogUrl).catch(() => []),
      // Built-in project roles are listed read-only so they can be compared
      // with custom ones before assigning them.
      this.projectId
        ? this.loadEndpoint(`/api/project/${this.projectId}/roles/all`).catch(() => [])
        : Promise.resolve([]),
    ]);
    this.projectPermissionCatalog = catalog;
    this.items = [...items, ...(all || []).filter((role) => role.builtin)];
  },
};
