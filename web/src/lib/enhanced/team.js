const enhancedMethods = {
  roleName(role) {
    return (this.userRoles.find((candidate) => candidate.value === role) || {}).name || role;
  },
  rolePermissions(role) {
    return (this.userRoles.find((candidate) => candidate.value === role) || {}).permissions || 0;
  },
};

export default enhancedMethods;
