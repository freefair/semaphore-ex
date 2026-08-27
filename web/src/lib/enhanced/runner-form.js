const enhancedMethods = {
  onRegistrationPolicyChange(policy) {
    if (policy === 'secure' && this.isNew) {
      this.item.registered = false;
    }
  },
};

export default enhancedMethods;
