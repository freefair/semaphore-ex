// Sections of the Administration area. Visibility mirrors the backend
// permission each page needs; the backend still enforces every request.
export const ADMIN_SECTIONS = [
  {
    key: 'users',
    path: '/admin/users',
    icon: 'mdi-account-multiple',
    label: 'adminUsers',
    visible: (access) => access.manageUsers,
  },
  {
    key: 'roles',
    path: '/admin/roles',
    icon: 'mdi-account-cog',
    label: 'adminRoles',
    visible: (access) => access.manageRoles,
  },
  {
    key: 'authentication',
    path: '/admin/authentication',
    icon: 'mdi-shield-key',
    label: 'adminAuthentication',
    visible: (access) => access.admin,
  },
  {
    key: 'group-mappings',
    path: '/admin/group-mappings',
    icon: 'mdi-account-group',
    label: 'adminGroupMappings',
    visible: (access) => access.admin,
  },
  {
    key: 'notifications',
    path: '/admin/notifications',
    icon: 'mdi-bell-ring',
    label: 'adminNotifications',
    visible: (access) => access.manageSystem,
  },
  {
    key: 'audit-log',
    path: '/admin/audit-log',
    icon: 'mdi-shield-search',
    label: 'adminAuditLog',
    visible: (access) => access.readAuditLog,
  },
  {
    key: 'audit-webhooks',
    path: '/admin/audit-webhooks',
    icon: 'mdi-webhook',
    label: 'adminGovernance',
    visible: (access) => access.governance,
  },
  {
    key: 'credentials',
    path: '/admin/credentials',
    icon: 'mdi-key-chain',
    label: 'adminCredentials',
    visible: (access) => access.credentials,
  },
  {
    key: 'secret-storages',
    path: '/admin/secret-storages',
    icon: 'mdi-safe-square-outline',
    label: 'adminSecretStorages',
    visible: (access) => access.admin,
  },
  {
    key: 'workflows',
    path: '/admin/workflows',
    icon: 'mdi-sitemap',
    label: 'adminWorkflows',
    visible: (access) => access.manageSystem,
  },
  {
    key: 'runners',
    path: '/admin/runners',
    icon: 'mdi-cogs',
    label: 'adminRunners',
    visible: (access) => access.manageSystem,
  },
  {
    key: 'cluster',
    path: '/admin/cluster',
    icon: 'mdi-server-network',
    label: 'adminCluster',
    visible: (access) => access.manageSystem,
  },
  {
    key: 'tasks',
    path: '/admin/tasks',
    icon: 'mdi-check-all',
    label: 'adminActiveTasks',
    visible: (access) => access.manageSystem,
  },
  {
    key: 'system',
    path: '/admin/system',
    icon: 'mdi-information-outline',
    label: 'adminSystemInformation',
    visible: (access) => access.manageSystem,
  },
];

// Paths of the pages before the Administration area existed. Bookmarks and
// links keep working through these redirects.
export const LEGACY_ADMIN_REDIRECTS = {
  '/users': '/admin/users',
  '/roles': '/admin/roles',
  '/role-mappings': '/admin/group-mappings',
  '/audit-log': '/admin/audit-log',
  '/audit-webhooks': '/admin/audit-webhooks',
  '/global-credentials': '/admin/credentials',
  '/runners': '/admin/runners',
  '/cluster': '/admin/cluster',
  '/tasks': '/admin/tasks',
};

export function adminAccess(context) {
  return {
    admin: Boolean(context.user?.admin),
    manageUsers: Boolean(context.canManageGlobalUsers),
    manageRoles: Boolean(context.canManageGlobalRoles),
    manageSystem: Boolean(context.canManageGlobalSystem),
    readAuditLog: Boolean(context.user?.can_read_audit_log),
    governance: Boolean(context.canAccessGlobalGovernance),
    credentials: Boolean(context.canAccessGlobalCredentials),
  };
}

export function visibleAdminSections(access) {
  return ADMIN_SECTIONS.filter((section) => section.visible(access));
}
