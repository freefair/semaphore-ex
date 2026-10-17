const enhancedMethods = {
  memberPermissions(member) {
    return (member.roles || []).reduce((permissions, slug) => {
      const role = this.roles.find((candidate) => candidate.slug === slug);
      return permissions | (role?.permissions || 0);
    }, 0);
  },
};

export default enhancedMethods;
