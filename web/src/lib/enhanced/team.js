import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import { permissionLabel } from '@/lib/enhanced/project-access';

const enhancedMethods = {
  // Feedback for a role change appears in the member's row, next to the
  // control that caused it; a success note fades, an error stays until the
  // next change.
  setRowStatus(memberId, type, text) {
    this.rowStatus = { ...this.rowStatus, [memberId]: { type, text } };
    if (type === 'success') {
      setTimeout(() => {
        if (this.rowStatus[memberId]?.text === text) {
          const { [memberId]: removed, ...rest } = this.rowStatus;
          this.rowStatus = rest;
        }
      }, 4000);
    }
  },

  // A member's granted permission IDs: the server's effective list when it
  // sends one, otherwise the union of the assigned and mapped roles' lists.
  memberPermissionIds(member) {
    if (Array.isArray(member.permission_ids)) {
      return member.permission_ids;
    }
    const roleSlugs = [
      ...(member.roles || []),
      ...(member.mapped_roles || []).map((mapping) => mapping.role),
    ];
    const ids = new Set();
    roleSlugs.forEach((slug) => {
      const role = (this.roles || []).find((candidate) => candidate.slug === slug);
      (role?.permission_ids || []).forEach((id) => ids.add(id));
    });
    return [...ids];
  },

  canInspectPermissions(member) {
    return member.id === this.userId || this.can(this.USER_PERMISSIONS.manageProjectUsers);
  },

  async loadPermissionCatalog() {
    try {
      this.permissionCatalog = (await axios({
        method: 'get',
        url: `/api/project/${this.projectId}/roles/permissions`,
        responseType: 'json',
      })).data;
    } catch {
      this.permissionCatalog = [];
    }
  },

  async showEffectivePermissions(member) {
    this.effective = {
      member, loading: true, error: null, permissions: [], sources: [],
    };
    try {
      const { data } = await axios({
        method: 'get',
        url: `/api/project/${this.projectId}/users/${member.id}/effective-permissions`,
        responseType: 'json',
      });
      this.effective = {
        member,
        loading: false,
        error: null,
        permissions: data.permissions || [],
        sources: data.sources || [],
      };
    } catch (err) {
      this.effective = {
        member,
        loading: false,
        error: this.$t('permEffectiveLoadFailed'),
        detail: getErrorMessage(err),
        permissions: [],
        sources: [],
      };
    }
  },

  permissionDescription(permissionId) {
    return permissionLabel(permissionId, this.permissionCatalog, this.$t);
  },

  effectiveSourcesFor(permissionId) {
    return (this.effective?.sources || [])
      .filter((source) => source.permission === permissionId)
      .map((source) => source.role_name || source.role_id);
  },
};

export default enhancedMethods;
