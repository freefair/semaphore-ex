export const enhancedComputed = {
  saveButtonText() {
    return this.$t(this.TEMPLATE_TYPE_ACTION_TITLES[this.template?.type || '']);
  },
};

export const enhancedMethods = {
  handlePreflight(clearSaveRequest) {
    clearSaveRequest();
  },
};
