export default function createEnhancedState() {
  return {
    globalRoles: [],
    globalRoleAssignments: [],
    globalPermissionCatalog: [],
    effectiveGlobalPermissions: { permissions: 0, decisions: [] },
    newGlobalRoleId: null,
    globalRolesLoading: false,
    globalRoleError: null,
  };
}
