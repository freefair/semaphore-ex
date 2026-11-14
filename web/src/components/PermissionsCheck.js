import { hasProjectPermission } from '@/lib/enhanced/project-access';

export default {
  props: {
    userPermissions: Number,
    isAdmin: Boolean,
  },

  inject: {
    projectAccess: { default: null },
  },

  methods: {

    // Project-level decisions use the server's permission ID list (provided by
    // App). Objects that carry their own small template permission mask keep
    // deciding on that mask.
    can(permission, item = 'item' in this ? this.item : undefined) {
      if (this.isAdmin || (this.projectAccess && this.projectAccess.admin)) {
        return true;
      }

      if (item && item.permissions != null && typeof permission === 'number') {
        // eslint-disable-next-line no-bitwise
        return (item.permissions & permission) === permission;
      }

      return hasProjectPermission((this.projectAccess || {}).ids, permission);
    },
  },
};
