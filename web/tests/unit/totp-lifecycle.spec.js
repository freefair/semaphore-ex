import { expect } from 'chai';
import axios from 'axios';
import TotpEnrollmentPanel from '@/components/TotpEnrollmentPanel.vue';
import SystemInfoDialog from '@/components/SystemInfoDialog.vue';
import Auth from '@/views/Auth.vue';
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
      enrollmentCeremony: null,
      enrollmentReauthentication: 'proof',
      enrollmentCode: '654321',
      enrollmentRecoveryStored: true,
      enrollmentStage: 'confirm',
      async runEnrollment(operation) { await operation(); },
      redirectAfterLogin() { this.redirected = true; },
    };

    await Auth.methods.beginRequiredEnrollment.call(context);
    await Auth.methods.confirmRequiredEnrollment.call(context);
    expect(context.enrollmentStage).to.equal('acknowledge');
    await Auth.methods.acknowledgeRequiredRecovery.call(context);
    expect(context.redirected).to.equal(true);
    expect(requests.map(({ url }) => url)).to.deep.equal([
      '/api/auth/totp/enroll',
      '/api/auth/totp/enroll/23/confirm',
      '/api/auth/totp/enroll/23/recovery-codes/acknowledge',
    ]);
  });

  it('loads and persists administrator rollout configuration with selected users', async () => {
    axios.get = async (url) => ({
      data: {
        '/api/capabilities/totp': { state: 'required_selected', selected_user_ids: [4] },
        '/api/users': [{ id: 4, username: 'local', external: false }],
        '/api/capabilities/totp/transitions': [{ id: 1, to_state: 'required_selected' }],
      }[url],
    });
    let saved;
    axios.put = async (url, data) => { saved = { url, data }; };
    const context = {
      totpDecision: { state: 'optional' },
      totpRollout: { state: 'disabled', selected_user_ids: [] },
      totpUsers: [],
      totpTransitions: [],
      totpRolloutError: null,
      totpRolloutSaving: false,
    };
    const emitted = [];
    context.$emit = (event) => emitted.push(event);
    context.loadTotpRollout = SystemInfoDialog.methods.loadTotpRollout.bind(context);

    await context.loadTotpRollout();
    expect(context.totpRollout.selected_user_ids).to.deep.equal([4]);
    await SystemInfoDialog.methods.saveTotpRollout.call(context);
    expect(saved.url).to.equal('/api/capabilities/totp');
    expect(saved.data.state).to.equal('required_selected');
    expect(emitted).to.deep.equal(['totp-rollout-updated']);
  });
});
