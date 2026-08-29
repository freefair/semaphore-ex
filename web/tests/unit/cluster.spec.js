import { expect } from 'chai';
import Cluster from '@/views/Cluster.vue';

describe('cluster dashboard node states', () => {
  it('uses explicit labels and treatments instead of presenting every live node as ready', () => {
    const context = { $t: (key) => key };
    expect(Cluster.methods.nodeState.call(context, { alive: true, ready: true })).to.equal('clusterNodeReady');
    expect(Cluster.methods.nodeState.call(context, { alive: false, compatibility_state: 'stale' })).to.equal('clusterNodeStale');
    expect(Cluster.methods.nodeState.call(context, { alive: true, compatibility_state: 'draining' })).to.equal('clusterNodeDraining');
    expect(Cluster.methods.nodeState.call(context, { alive: true, compatibility_state: 'incompatible_schema' })).to.equal('clusterNodeIncompatible');
    expect(Cluster.methods.nodeStateColor({ alive: true, ready: true })).to.equal('success');
    expect(Cluster.methods.nodeStateColor({ alive: false, compatibility_state: 'stale' })).to.equal('warning');
    expect(Cluster.methods.nodeVersion({})).to.equal('—');
    expect(Cluster.methods.nodeVersion({ version: 'undefined' })).to.equal('—');
    expect(Cluster.methods.nodeVersion({ version: 'undefined-00000000-' })).to.equal('—');
  });

  it('keeps the clear action disabled until the HA status has loaded', () => {
    expect(
      Cluster.computed.canClearClusterTasks.call({
        features: { high_availability: true },
        status: null,
      }),
    ).to.equal(false);
    expect(
      Cluster.computed.canClearClusterTasks.call({
        features: { high_availability: true },
        status: { ha_enabled: true },
      }),
    ).to.equal(true);
  });
});
