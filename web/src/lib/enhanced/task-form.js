import axios from 'axios';

export const enhancedComputed = {
  deploymentWindowOverrideCategories() {
    return [
      { text: this.$t('deploymentWindowOverrideIncident'), value: 'incident' },
      { text: this.$t('deploymentWindowOverrideSecurity'), value: 'security' },
      { text: this.$t('deploymentWindowOverrideCustomerImpact'), value: 'customer_impact' },
    ];
  },
  deploymentWindowOverrideReady() {
    return Boolean(this.deploymentWindowBlock
        && this.deploymentWindowOverrideConfirmed
        && ['incident', 'security', 'customer_impact']
          .includes(this.deploymentWindowOverrideCategory)
        && /^[A-Z0-9]{2,16}-[1-9][0-9]{0,9}$/
          .test(this.deploymentWindowOverrideReference));
  },
  deploymentWindowBlockMessage() {
    return this.$t(`deploymentWindowBlocked_${this.deploymentWindowBlock?.reason || 'unknown'}`);
  },
};

export const enhancedMethods = {
  taskSavePayload() {
    return {
      ...this.item,
      project_id: this.projectId,
    };
  },
  taskStartPayload(payload) {
    if (!this.deploymentWindowOverrideReady) return payload;
    return {
      ...payload,
      deployment_window_override: {
        category: this.deploymentWindowOverrideCategory,
        reference: this.deploymentWindowOverrideReference,
      },
    };
  },
  isDeploymentWindowBlock(err) {
    return err?.response?.status === 409
        && err?.response?.data?.state === 'blocked';
  },
  adoptDeploymentWindowBlock(decision) {
    this.deploymentWindowBlock = decision;
    this.deploymentWindowOverrideCategory = null;
    this.deploymentWindowOverrideReference = '';
    this.deploymentWindowOverrideConfirmed = false;
    this.formError = null;
  },
  clearDeploymentWindowBlock() {
    this.deploymentWindowBlock = null;
    this.deploymentWindowOverrideCategory = null;
    this.deploymentWindowOverrideReference = '';
    this.deploymentWindowOverrideConfirmed = false;
  },
  formatDeploymentWindowDate(value) {
    return new Date(value).toLocaleString();
  },
  isExecutionPreflightUnavailable(err) {
    const response = err?.response;
    return response?.status === 404
        && response?.data?.error === 'CAPABILITY_DENIED'
        && response?.data?.capability === 'execution_preflight';
  },
  async submitTaskPayload(payload, headers = {}) {
    const item = (await axios({
      method: 'post',
      url: this.getItemsUrl(),
      responseType: 'json',
      data: payload,
      headers,
    })).data;
    await this.afterSave(item);
    this.$emit('save', { item: item || this.item, action: this.getSaveAction() });
    return item || this.item;
  },
};
