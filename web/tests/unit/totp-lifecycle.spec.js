import { expect } from 'chai';
import axios from 'axios';
import TotpEnrollmentPanel from '@/components/TotpEnrollmentPanel.vue';
import TotpRequiredEnrollment from '@/components/TotpRequiredEnrollment.vue';
import { capabilityStateColor } from '@/lib/capabilities';

describe('TOTP lifecycle UI contracts', () => {
  let originalGet;
  let originalPost;
  let originalPut;

  beforeEach(() => {
    originalGet = axios.get;
    originalPost = axios.post;
    originalPut = axios.put;
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.post = originalPost;
    axios.put = originalPut;
  });

  it('renders every rollout state with an explicit visual treatment', () => {
    expect(capabilityStateColor('shadow')).to.equal('info');
    expect(capabilityStateColor('optional')).to.equal('success');
    expect(capabilityStateColor('required_selected')).to.equal('warning');
    expect(capabilityStateColor('required')).to.equal('error');
  });

  it('keeps enrollment pending through confirmation and activates only after acknowledgement', async () => {
    const requests = [];
    axios.post = async (url, data) => {
      requests.push({ url, data });
      if (url.endsWith('/2fas/totp')) {
        return { data: { id: 17, recovery_codes: ['ONE', 'TWO'] } };
      }
      if (url.endsWith('/confirm')) {
        return { data: { enrollment_state: 'pending_recovery_ack' } };
      }
      return { data: { enrollment_state: 'active', recovery_codes_remaining: 2 } };
    };
    const context = {
      itemId: 4,
      reauthentication: 'proof',
      passcode: '123456',
      recoveryStored: true,
      ceremony: null,
      status: { enrollment_state: 'none' },
      error: null,
      saving: false,
      async run(operation) { await operation(); },
      async loadStatus() { return undefined; },
    };

    await TotpEnrollmentPanel.methods.beginEnrollment.call(context);
    expect(context.ceremony.id).to.equal(17);
    await TotpEnrollmentPanel.methods.confirmEnrollment.call(context);
    expect(context.status.enrollment_state).to.equal('pending_recovery_ack');
    await TotpEnrollmentPanel.methods.acknowledgeRecoveryCodes.call(context);
    expect(context.status.enrollment_state).to.equal('active');
    expect(context.ceremony).to.equal(null);
    expect(requests.map(({ url }) => url)).to.deep.equal([
      '/api/users/4/2fas/totp',
      '/api/users/4/2fas/totp/17/confirm',
      '/api/users/4/2fas/totp/17/recovery-codes/acknowledge',
    ]);
  });

  it('uses the restricted auth enrollment endpoints for required users', async () => {
    const requests = [];
    axios.post = async (url, data) => {
      requests.push({ url, data });
      if (url === '/api/auth/totp/enroll') {
        return { data: { id: 23, recovery_codes: ['RECOVERY'] } };
      }
      return { data: {} };
    };
    const context = {
      ceremony: null,
      reauthentication: 'proof',
      passcode: '654321',
      recoveryStored: true,
      stage: 'confirm',
      async run(operation) { await operation(); },
      $emit(event) { if (event === 'complete') this.completed = true; },
    };

    await TotpRequiredEnrollment.methods.beginEnrollment.call(context);
    await TotpRequiredEnrollment.methods.confirmEnrollment.call(context);
    expect(context.stage).to.equal('acknowledge');
    await TotpRequiredEnrollment.methods.acknowledgeRecovery.call(context);
    expect(context.completed).to.equal(true);
    expect(requests.map(({ url }) => url)).to.deep.equal([
      '/api/auth/totp/enroll',
      '/api/auth/totp/enroll/23/confirm',
      '/api/auth/totp/enroll/23/recovery-codes/acknowledge',
    ]);
  });

  it('formats and exports only the current enrollment ceremony values', () => {
    const ceremony = {
      provisioning_uri: 'otpauth://totp/Semaphore:test?secret=JBSWY3DPEHPK3PXP&issuer=Semaphore',
      recovery_codes: ['ALPHA', 'BRAVO'],
    };
    const context = { ceremony };
    const secret = TotpEnrollmentPanel.computed.secret.call(context);
    context.secret = secret;

    expect(secret).to.equal('JBSWY3DPEHPK3PXP');
    expect(TotpEnrollmentPanel.computed.groupedSecret.call(context)).to.equal('JBSW Y3DP EHPK 3PXP');
    expect(TotpEnrollmentPanel.computed.recoveryCodesText.call(context)).to.equal('ALPHA\nBRAVO');
    expect(TotpRequiredEnrollment.computed.secret.call({ ceremony })).to.equal(secret);
  });
});
