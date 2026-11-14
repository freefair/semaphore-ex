import { hasProjectPermission } from '@/lib/enhanced/project-access';

// Mixin for components that decide on project permissions without the
// PermissionsCheck object logic. App provides `projectAccess`.
export default {
  inject: {
    projectAccess: { default: null },
  },
  methods: {
    projectCan(permission) {
      if (this.isAdmin || (this.projectAccess && this.projectAccess.admin)) {
        return true;
      }
      return hasProjectPermission((this.projectAccess || {}).ids, permission);
    },
  },
};
