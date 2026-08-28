import { expect } from 'chai';
import {
  capabilityStateColor,
  findCapabilityDecision,
} from '@/lib/capabilities';

describe('capability presentation', () => {
  it('uses the backend decision and distinguishes every lifecycle state', () => {
    const decision = findCapabilityDecision({
      capabilities: {
        capabilities: [{ id: 'lifecycle_test', state: 'disabled', reason: 'disabled_by_admin' }],
      },
    }, 'lifecycle_test');

    expect(decision.reason).to.equal('disabled_by_admin');
    expect(capabilityStateColor('active')).to.equal('success');
    expect(capabilityStateColor('unavailable')).to.equal('grey');
    expect(capabilityStateColor('disabled')).to.equal('warning');
    expect(capabilityStateColor('expired')).to.equal('warning');
    expect(capabilityStateColor('read_only')).to.equal('info');
    expect(capabilityStateColor('insufficient_permission')).to.equal('error');
    expect(capabilityStateColor('shadow')).to.equal('info');
    expect(capabilityStateColor('optional')).to.equal('success');
    expect(capabilityStateColor('required_selected')).to.equal('warning');
    expect(capabilityStateColor('required')).to.equal('error');
  });

  it('returns no decision when the server did not provide one', () => {
    expect(findCapabilityDecision({}, 'lifecycle_test')).to.equal(null);
  });
});
