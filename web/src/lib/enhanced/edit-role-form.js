// Global permissions are a small mask (bits below 2^9), so the form edits that
// mask directly. Project permissions are edited only as the ID list
// `permission_ids`: their mask uses bits up to 2^54, which a browser number
// does not keep exactly, so the form never sends a project mask back.
const enhancedMethods = {
  hasPermission(permission) {
    return ((this.item.global_permissions || 0) & permission) === permission;
  },
  setPermission(permission, enabled) {
    if (enabled) {
      this.item.global_permissions = (this.item.global_permissions || 0) | permission;
    } else {
      this.item.global_permissions = (this.item.global_permissions || 0) & ~permission;
    }
  },
  async beforeLoadData() {
    if (this.projectId) {
      this.permissionCatalog = await this.loadEndpoint(`/api/project/${this.projectId}/roles/permissions`);
      return;
    }
    [this.permissionCatalog, this.projectPermissionCatalog] = await Promise.all([
      this.loadEndpoint('/api/roles/permissions'),
      this.loadEndpoint('/api/roles/project-permissions'),
    ]);
  },
  afterLoadData() {
    if (!Array.isArray(this.item.permission_ids)) {
      this.item.permission_ids = [];
    }
  },
  beforeSave() {
    // permission_ids is authoritative; a rounded mask next to it would conflict.
    delete this.item.permissions;
  },
};

export default enhancedMethods;
