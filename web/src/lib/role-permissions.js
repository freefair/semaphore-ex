export function hasGlobalPermission(systemInfo, permission, isAdmin = false) {
  if (isAdmin || systemInfo?.admin === true) return true;
  // Both API projections carry the server's effective mask; legacy EX includes provenance.
  const permissions = systemInfo?.global_permissions;
  const mask = typeof permissions === 'number' ? permissions : permissions?.permissions || 0;
  return (mask & permission) === permission;
}

export function templatePermissionEffect(item, permission) {
  if (((item?.denied_permissions || 0) & permission) === permission) return 'deny';
  if (((item?.allowed_permissions || 0) & permission) === permission) return 'allow';
  return 'inherit';
}

export function setTemplatePermissionEffect(item, permission, effect) {
  const allowed = (item.allowed_permissions || 0) & ~permission;
  const denied = (item.denied_permissions || 0) & ~permission;

  return {
    ...item,
    allowed_permissions: effect === 'allow' ? allowed | permission : allowed,
    denied_permissions: effect === 'deny' ? denied | permission : denied,
  };
}

export function permissionEntries(catalog, mask) {
  return (catalog || []).filter((entry) => ((mask || 0) & entry.permission) === entry.permission);
}
