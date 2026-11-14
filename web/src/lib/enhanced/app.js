import { hasProjectPermission } from '@/lib/enhanced/project-access';
import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';
import { adminAccess, visibleAdminSections } from '@/lib/enhanced/admin-sections';

const enhancedComputed = {
  hasAdministrationAccess() {
    return visibleAdminSections(adminAccess(this)).length > 0;
  },
  canReadTaskGroups() {
    return this.user?.admin
      || hasProjectPermission(this.userRole?.permission_ids, USER_PERMISSIONS.readTaskGroups);
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

export default enhancedComputed;
