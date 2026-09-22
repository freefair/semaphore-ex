import canViewProjectResources from '@/lib/project-permissions';
import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

export const enhancedComputed = {
  canReadTaskGroups() {
    return this.user?.admin || ((this.userRole?.permissions || 0)
      & USER_PERMISSIONS.readTaskGroups) === USER_PERMISSIONS.readTaskGroups;
  },
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
  canManageGlobalPolicyGuardrails() {
    return hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.managePolicyGuardrails,
      this.user?.admin,
    );
  },
  canRollbackGlobalPolicyGuardrails() {
    return hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.rollbackPolicyGuardrails,
      this.user?.admin,
    );
  },
  canAccessGlobalGovernance() {
    return this.canManageGlobalSystem
        || this.canManageGlobalPolicyGuardrails
        || this.canRollbackGlobalPolicyGuardrails;
  },
  canAccessGlobalCredentials() {
    return hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.manageCredentialMetadata,
      this.user?.admin,
    ) || hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.rotateCredentials,
      this.user?.admin,
    ) || hasGlobalPermission(
      this.systemInfo,
      GLOBAL_PERMISSIONS.grantCredentials,
      this.user?.admin,
    );
  },
};

export const enhancedMethods = {
  canViewProjectResources() {
    return canViewProjectResources(this.user, this.userRole);
  },
};
