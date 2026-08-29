import canViewProjectResources from '@/lib/project-permissions';

const enhancedMethods = {
  canViewProjectResources() {
    return canViewProjectResources(this.user, this.userRole);
  },
};

export default enhancedMethods;
