const enhancedMethods = {
  permissionLabel(label) {
    return this.scope === 'default' || this.scope === 'workflow' ? this.$t(label) : label;
  },
};

export default enhancedMethods;
