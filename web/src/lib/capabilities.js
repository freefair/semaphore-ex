export function findCapabilityDecision(systemInfo, capabilityId) {
  const decisions = systemInfo?.capabilities?.capabilities;
  if (!Array.isArray(decisions)) {
    return null;
  }
  return decisions.find(({ id }) => id === capabilityId) || null;
}

export function capabilityStateColor(state) {
  const colors = {
    active: 'success',
    unavailable: 'grey',
    disabled: 'warning',
    expired: 'warning',
    read_only: 'info',
    insufficient_permission: 'error',
    shadow: 'info',
    optional: 'success',
    required_selected: 'warning',
    required: 'error',
  };
  return colors[state] || 'grey';
}
