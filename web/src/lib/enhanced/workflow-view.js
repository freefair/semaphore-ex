const enhancedMethods = {
  hasRunInputs(workflow) {
    return (workflow.parameters || []).length > 0
        || (workflow.nodes || []).some((node) => {
          const policy = node.override_policy || {};
          return (policy.inventory_ids || []).length
            || (policy.environment_ids || []).length
            || policy.allow_arguments
            || policy.allow_branch;
        });
  },
};

export default enhancedMethods;
