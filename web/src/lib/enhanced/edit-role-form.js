const enhancedMethods = {
  hasPermission(permission) {
    return (this.item.permissions & permission) === permission;
  },
  setPermission(permission, enabled) {
    if (enabled) {
      this.item.permissions |= permission;
    } else {
      this.item.permissions &= ~permission;
    }
  },
  async beforeLoadData() {
    if (this.projectId) {
      this.permissionCatalog = await this.loadEndpoint(
        `/api/project/${this.projectId}/roles/permissions`,
      );
    }
  },
};

export default enhancedMethods;
