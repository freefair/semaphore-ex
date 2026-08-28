import { expect } from 'chai';
import axios from 'axios';
import LdapCapabilityPanel from '@/components/LdapCapabilityPanel.vue';
import Auth from '@/views/Auth.vue';

describe('LDAP capability UI contracts', () => {
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

  it('loads providers without creating readable bind secret state', async () => {
    axios.get = async (url) => ({
      data: url === '/api/capabilities/ldap'
        ? [{
          id: 'corp',
          display_name: 'Corporate LDAP',
          state: 'selected_users',
          bind_password_configured: true,
          selected_user_ids: [12],
          eligible_user_ids: [12],
          readiness: { status: 'ready', recovery: true },
        }]
        : [{ id: 12, username: 'linked-user', external: true }, { id: 13, username: 'other' }],
    });
    const context = {
      providers: [],
      users: [],
      selectedProviderID: '',
      form: LdapCapabilityPanel.methods.emptyProvider(),
      bindPassword: '',
      error: '',
      loading: false,
      applyProvider: LdapCapabilityPanel.methods.applyProvider,
      selectProvider: LdapCapabilityPanel.methods.selectProvider,
      errorMessage: LdapCapabilityPanel.methods.errorMessage,
    };

    await LdapCapabilityPanel.methods.load.call(context);

    expect(context.selectedProviderID).to.equal('corp');
    expect(context.form.bind_password_configured).to.equal(true);
    expect(context.bindPassword).to.equal('');
    expect(JSON.stringify(context)).not.to.include('bind-secret');
    expect(LdapCapabilityPanel.computed.eligibleUsers.call(context).map((user) => user.id))
      .to.deep.equal([12]);
  });

  it('sends bind credentials once and clears them after configuration', async () => {
    let payload;
    axios.put = async (url, data) => {
      payload = { url, data };
      return { data: { ...data, bind_password_configured: true } };
    };
    const context = {
      form: { ...LdapCapabilityPanel.methods.emptyProvider(), id: 'corp' },
      bindPassword: 'write-only-secret',
      saving: false,
      error: '',
      providers: [],
      selectedProviderID: 'corp',
      applyProvider: LdapCapabilityPanel.methods.applyProvider,
      configurationPayload: LdapCapabilityPanel.methods.configurationPayload,
      errorMessage: LdapCapabilityPanel.methods.errorMessage,
    };

    await LdapCapabilityPanel.methods.save.call(context);

    expect(payload.url).to.equal('/api/capabilities/ldap');
    expect(payload.data.bind_password).to.equal('write-only-secret');
    expect(context.bindPassword).to.equal('');
    expect(context.form).not.to.have.property('bind_password');
  });

  it('locks connection changes while directory login is exposed', () => {
    expect(LdapCapabilityPanel.computed.configurationLocked.call({
      selectedProvider: { state: 'active' },
    })).to.equal(true);
    expect(LdapCapabilityPanel.computed.configurationLocked.call({
      selectedProvider: { state: 'selected_users' },
    })).to.equal(true);
    expect(LdapCapabilityPanel.computed.configurationLocked.call({
      selectedProvider: { state: 'shadow' },
    })).to.equal(false);
  });

  it('tests both directory access and local recovery before applying lifecycle state', async () => {
    const requests = [];
    axios.post = async (url, data) => {
      requests.push({ url, data });
      return {
        data: {
          status: 'ready', connection: true, search: true, bind: true, recovery: true,
        },
      };
    };
    axios.put = async (url, data) => {
      requests.push({ url, data });
      return { data: { id: 'corp', state: data.state, selected_user_ids: data.selected_user_ids } };
    };
    const context = {
      selectedProviderID: 'corp',
      testUsername: 'probe',
      testPassword: 'directory-proof',
      recoveryAdminUserID: 7,
      recoveryAdminPassword: 'local-proof',
      readiness: null,
      testLoading: false,
      stateSaving: false,
      error: '',
      form: {
        ...LdapCapabilityPanel.methods.emptyProvider(),
        id: 'corp',
        state: 'selected_users',
        selected_user_ids: [12],
      },
      providers: [],
      selectedProvider: { id: 'corp', selected_user_ids: [12] },
      applyProvider: LdapCapabilityPanel.methods.applyProvider,
      errorMessage: LdapCapabilityPanel.methods.errorMessage,
      async load() { return undefined; },
    };

    await LdapCapabilityPanel.methods.testConnection.call(context);
    expect(context.recoveryAdminPassword).to.equal('');
    await LdapCapabilityPanel.methods.applyState.call(context);

    expect(requests).to.deep.equal([
      {
        url: '/api/capabilities/ldap/test',
        data: {
          provider_id: 'corp',
          username: 'probe',
          password: 'directory-proof',
          recovery_admin_user_id: 7,
          recovery_admin_password: 'local-proof',
        },
      },
      {
        url: '/api/capabilities/ldap/state',
        data: { provider_id: 'corp', state: 'selected_users', selected_user_ids: [12] },
      },
    ]);
  });

  it('shows explicit provider outage and local recovery messages on login', () => {
    expect(Auth.methods.authenticationErrorMessage.call({ $t: (key) => key }, {
      response: { status: 503, data: { error: 'LDAP_PROVIDER_UNAVAILABLE' } },
    })).to.equal('ldapProviderUnavailable');
    expect(Auth.methods.authenticationErrorMessage.call({ $t: (key) => key }, {
      response: { status: 429, data: { error: 'LDAP_THROTTLED' } },
    })).to.equal('ldapLoginThrottled');
  });

  it('turns invalid readiness credentials into an actionable message', () => {
    expect(LdapCapabilityPanel.methods.errorMessage({
      response: { data: { error: 'LDAP_INVALID_CREDENTIALS' } },
    })).to.equal('The directory credentials were rejected. Check the test username and password.');
  });
});
