import canViewProjectResources from '@/lib/project-permissions';
import { GLOBAL_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

export const enhancedComputed = {
  canManageGlobalUsers() {
    return hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.manageUsers,
      this.user?.admin,
    );
  },
  canManageGlobalRoles() {
    return hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.manageRoles,
      this.user?.admin,
    );
  },
  canManageGlobalSystem() {
    return hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.manageSystem,
      this.user?.admin,
    );
  },
};

export const enhancedMethods = {
  canViewProjectResources() {
    return canViewProjectResources(this.user, this.userRole);
  },
};
