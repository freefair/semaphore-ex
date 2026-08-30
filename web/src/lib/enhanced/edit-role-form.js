const enhancedMethods = {
  hasPermission(permission) {
    const field = this.projectId ? 'permissions' : 'global_permissions';
    return ((this.item[field] || 0) & permission) === permission;
  },
  setPermission(permission, enabled) {
    const field = this.projectId ? 'permissions' : 'global_permissions';
    if (enabled) {
      this.item[field] = (this.item[field] || 0) | permission;
    } else {
      this.item[field] = (this.item[field] || 0) & ~permission;
    }
  },
  async beforeLoadData() {
    this.permissionCatalog = await this.loadEndpoint(
      this.projectId
        ? `/api/project/${this.projectId}/roles/permissions`
        : '/api/roles/permissions',
    );
  },
};

export default enhancedMethods;
