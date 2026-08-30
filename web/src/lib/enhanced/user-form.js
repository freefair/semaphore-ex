import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
  availableGlobalRoles() {
    const assigned = new Set(this.globalRoleAssignments.map(({ role_id: roleId }) => roleId));
    return this.globalRoles.filter(({ id }) => !assigned.has(id));
  },
};

export const enhancedMethods = {
  async loadGlobalRoleData() {
    this.globalRolesLoading = true;
    this.globalRoleError = null;
    try {
      const [roles, assignments, effective, catalog] = await Promise.all([
        axios.get('/api/roles'),
        axios.get(`/api/users/${this.itemId}/global-roles`),
        axios.get(`/api/users/${this.itemId}/global-permissions`),
        axios.get('/api/roles/permissions'),
      ]);
      this.globalRoles = roles.data || [];
      this.globalRoleAssignments = assignments.data || [];
      this.effectiveGlobalPermissions = effective.data || { permissions: 0, decisions: [] };
      this.globalPermissionCatalog = catalog.data || [];
    } catch (error) {
      this.globalRoleError = getErrorMessage(error);
    } finally {
      this.globalRolesLoading = false;
    }
  },
  async addGlobalRoleAssignment() {
    if (!this.newGlobalRoleId) return;
    this.globalRolesLoading = true;
    this.globalRoleError = null;
    try {
      await axios.post(`/api/users/${this.itemId}/global-roles`, {
        role_id: this.newGlobalRoleId,
      });
      this.newGlobalRoleId = null;
      await this.loadGlobalRoleData();
    } catch (error) {
      this.globalRoleError = getErrorMessage(error);
      this.globalRolesLoading = false;
    }
  },
  async removeGlobalRoleAssignment(assignment) {
    this.globalRolesLoading = true;
    this.globalRoleError = null;
    try {
      await axios.delete(
        `/api/users/${this.itemId}/global-roles/${assignment.id}`
          + `?revision=${encodeURIComponent(assignment.revision)}`,
      );
      await this.loadGlobalRoleData();
    } catch (error) {
      this.globalRoleError = getErrorMessage(error);
      this.globalRolesLoading = false;
    }
  },
  globalPermissionDescription(permission) {
    const definition = this.globalPermissionCatalog.find(({ id }) => id === permission);
    return definition ? definition.description : permission;
  },
  globalPermissionProvenance(decision) {
    const provenance = decision.provenance || {};
    if (decision.allowed && provenance.role_name) {
      return `${provenance.effect} by ${provenance.role_name} (${provenance.scope})`;
    }
    return `Denied at ${provenance.scope || 'global'} scope`;
  },
};
