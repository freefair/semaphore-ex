import { expect } from 'chai';
import EnhancedSystemInfoPanel from '@/components/EnhancedSystemInfoPanel.vue';

describe('debug log filter diagnostics', () => {
  it('reads only the diagnostics returned by the authorized admin endpoint', () => {
    const diagnostics = {
      instance: 'node-a',
      configured: ['runner', 'task_*'],
      effective: ['runner', 'task_*'],
    };

    expect(EnhancedSystemInfoPanel.computed.debugFilter.call({
      diagnostics: { debug_filter: diagnostics },
    })).to.equal(diagnostics);
    expect(EnhancedSystemInfoPanel.computed.debugFilter.call({ diagnostics: {} })).to.equal(null);
  });

  it('describes the explicit empty configuration as the all-components default', () => {
    expect(EnhancedSystemInfoPanel.methods.debugFilterDefaultText({
      default: 'all',
      configured: [],
    })).to.equal('All components are captured by default.');
  });

  it('keeps rejected filter reasons visible to the operator', () => {
    expect(EnhancedSystemInfoPanel.methods.debugFilterRejectedText({
      entry: 'task*middle',
      reason: 'wildcard_must_be_terminal',
    })).to.equal('task*middle — wildcard must be terminal');
  });
});
