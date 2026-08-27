import { expect } from 'chai';
import SystemInfoDialog from '@/components/SystemInfoDialog.vue';

describe('debug log filter diagnostics', () => {
  it('reads only the diagnostics returned by the authorized admin endpoint', () => {
    const diagnostics = {
      instance: 'node-a',
      configured: ['runner', 'task_*'],
      effective: ['runner', 'task_*'],
    };

    expect(SystemInfoDialog.computed.debugFilter.call({
      info: { debug_filter: diagnostics },
    })).to.equal(diagnostics);
    expect(SystemInfoDialog.computed.debugFilter.call({ info: {} })).to.equal(null);
  });

  it('describes the explicit empty configuration as the all-components default', () => {
    expect(SystemInfoDialog.methods.debugFilterDefaultText({
      default: 'all',
      configured: [],
    })).to.equal('All components are captured by default.');
  });

  it('keeps rejected filter reasons visible to the operator', () => {
    expect(SystemInfoDialog.methods.debugFilterRejectedText({
      entry: 'task*middle',
      reason: 'wildcard_must_be_terminal',
    })).to.equal('task*middle — wildcard must be terminal');
  });
});
