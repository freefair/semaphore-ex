export function hasGlobalPermission(systemInfo, permission, isAdmin = false) {
  if (isAdmin) return true;
  const mask = systemInfo?.global_permissions?.permissions || 0;
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
