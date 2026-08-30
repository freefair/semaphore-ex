import axios from 'axios';

const enhancedMethods = {
  async beforeLoadItems() {
    await Promise.all([this.loadRoles(), this.loadEffectivePermissions()]);
  },
  getDeleteItemUrl(item) {
    return `${this.getSingleItemUrl()}?revision=${encodeURIComponent(item.revision)}`;
  },
  async loadEffectivePermissions() {
    const response = await axios.get(
      `/api/project/${this.projectId}/templates/${this.templateId}/permissions/effective`,
    );
    this.effectivePermissions = response.data;
  },
  permissionName(permissionId) {
    const catalogNames = {
      'template.read': 'Read template',
      'template.run': 'Run template',
      'template.edit': 'Edit template',
      'template.delete': 'Delete template',
    };
    return catalogNames[permissionId] || permissionId;
  },
  permissionProvenance(decision) {
    const provenance = decision.provenance || {};
    if (provenance.role_name) {
      return `${provenance.effect} by ${provenance.role_name} (${provenance.scope})`;
    }
    return `${decision.allowed ? 'Allowed' : 'Denied'} at ${provenance.scope || 'template'} scope`;
  },
};

export default enhancedMethods;
