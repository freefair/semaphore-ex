export const enhancedComputed = {
  saveButtonText() {
    return this.preflightReady
      ? this.$t('confirmExecution')
      : this.$t(this.TEMPLATE_TYPE_ACTION_TITLES[this.template?.type || '']);
  },
};

export const enhancedMethods = {
  handlePreflight(clearSaveRequest) {
    this.preflightReady = true;
    clearSaveRequest();
  },
};
