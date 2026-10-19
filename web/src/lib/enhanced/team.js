const enhancedMethods = {
  memberPermissions(member) {
    const roleSlugs = [
      ...(member.roles || []),
      ...(member.mapped_roles || []).map((mapping) => mapping.role),
    ];
    return roleSlugs.reduce((permissions, slug) => {
      const role = this.roles.find((candidate) => candidate.slug === slug);
      return permissions | (role?.permissions || 0);
    }, 0);
  },
};

export default enhancedMethods;
