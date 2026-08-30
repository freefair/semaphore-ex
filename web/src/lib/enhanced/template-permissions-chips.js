const enhancedMethods = {
  permissionLabel(label) {
    return this.scope === 'default' ? this.$t(label) : label;
  },
};

export default enhancedMethods;
