export const enhancedComputed = {
  saveButtonText() {
    if (this.isInventoryRefresh) return this.$t('hostsRefresh');
    return this.$t(this.TEMPLATE_TYPE_ACTION_TITLES[this.template?.type || '']);
  },
};

export const enhancedMethods = {
  handlePreflight(clearSaveRequest) {
    clearSaveRequest();
  },
};
