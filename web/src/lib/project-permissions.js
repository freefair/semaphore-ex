import { USER_PERMISSIONS } from '@/lib/constants';

export default function canViewProjectResources(user, userRole) {
  if ((user || {}).admin) {
    return true;
  }
  const permissions = (userRole || {}).permissions || 0;
  return (permissions & USER_PERMISSIONS.viewProjectResources)
    === USER_PERMISSIONS.viewProjectResources;
}
