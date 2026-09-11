export function createEnhancedState() {
  return {
    connectionTesting: false,
    connectionHealth: null,
    runtimeAuthMethods: [
      { value: 'token', text: 'Token' },
      { value: 'approle', text: 'AppRole' },
      { value: 'kubernetes', text: 'Kubernetes JWT' },
    ],
    runtimeSyncDirections: [
      { value: 'read_only', text: 'Read-only runtime resolution (recommended)' },
      { value: 'outbound', text: 'Outbound managed synchronization' },
    ],
    localKeys: [],
  };
}

export const enhancedWatch = {
  'item.sync_direction': {
    handler: function syncDirectionChanged(value) {
      if (!this.isRuntimeProvider) {
        return;
      }
      this.item.readonly = value !== 'outbound';
      if (value !== 'outbound') {
        this.item.sync_enabled = false;
        this.item.sync_interval = 0;
      }
    },
  },
};
