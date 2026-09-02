import axios from 'axios';

const enhancedMethods = {
  taskSavePayload() {
    return {
      ...this.item,
      project_id: this.projectId,
    };
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

export default enhancedMethods;
