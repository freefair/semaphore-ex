export const enhancedComputed = {
  canClearClusterTasks() {
    return Boolean(
      this.features && this.features.high_availability && this.status && this.status.ha_enabled,
    );
  },
};

export const enhancedMethods = {
  nodeState(node) {
    if (node.ready) return this.$t('clusterNodeReady');
    if (!node.alive || node.compatibility_state === 'stale') return this.$t('clusterNodeStale');
    if (node.compatibility_state === 'draining') return this.$t('clusterNodeDraining');
    return this.$t('clusterNodeIncompatible');
  },
  nodeStateColor(node) {
    if (node.ready) return 'success';
    if (!node.alive || node.compatibility_state === 'stale') return 'warning';
    if (node.compatibility_state === 'draining') return 'grey';
    return 'error';
  },
  nodeVersion(node) {
    if (!node.version || node.version === 'undefined' || node.version.startsWith('undefined-')) {
      return '—';
    }
    return node.version;
  },
  coordinatorState(coordinator) {
    if (coordinator.live_events === 'healthy') return this.$t('clusterLiveEventsHealthy');
    if (coordinator.live_events === 'degraded') return this.$t('clusterLiveEventsDegraded');
    return this.$t('clusterLiveEventsUnavailable');
  },
  coordinatorStateColor(coordinator) {
    if (coordinator.live_events === 'healthy') return 'success';
    if (coordinator.live_events === 'degraded') return 'warning';
    return 'grey';
  },
};
