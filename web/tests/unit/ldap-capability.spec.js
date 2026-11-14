import { expect } from 'chai';
import axios from 'axios';
import LdapProviderSetup from '@/components/enhanced/ldap/LdapProviderSetup.vue';
import LdapGroupMappingPanel from '@/components/enhanced/ldap/LdapGroupMappingPanel.vue';
import Auth from '@/views/Auth.vue';
import {
  applyLdapPreset,
  emptyLdapProvider,
  ldapErrorText,
  ldapStageStates,
  mappingIdFromName,
  readinessExpiry,
} from '@/lib/enhanced/ldap';
import mockAxios, { httpError } from './helpers/axiosMock';

const t = (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key);

function component(definition, overrides = {}) {
  const context = { ...definition.data.call({}), $t: t, ...overrides };
  Object.entries(definition.methods).forEach(([name, method]) => {
    if (!(name in overrides)) context[name] = method.bind(context);
  });
  Object.entries(definition.computed || {}).forEach(([name, getter]) => {
    if (!(name in overrides)) {
      Object.defineProperty(context, name, { get: getter.bind(context), configurable: true });
    }
  });
  return context;
}

describe('LDAP provider setup', () => {
  let original;

  beforeEach(() => {
    original = {
      get: axios.get, post: axios.post, put: axios.put, delete: axios.delete,
    };
  });

  afterEach(() => {
    Object.assign(axios, original);
  });

  it('loads providers without keeping a readable bind secret', async () => {
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
    const context = component(LdapProviderSetup);

    await context.load();

    expect(context.selectedProviderID).to.equal('corp');
    expect(context.form.bind_password_configured).to.equal(true);
    expect(context.bindPassword).to.equal('');
    expect(context.eligibleUsers.map((user) => user.id)).to.deep.equal([12]);
  });

  it('sends the bind password once, clears it and confirms the save', async () => {
    let payload;
    axios.put = async (url, data) => {
      payload = { url, data };
      return { data: { ...data, bind_password_configured: true } };
    };
    const context = component(LdapProviderSetup);
    context.form = { ...emptyLdapProvider(), id: 'corp' };
    context.bindPassword = 'write-only-secret';

    await context.save();

    expect(payload.url).to.equal('/api/capabilities/ldap');
    expect(payload.data.bind_password).to.equal('write-only-secret');
    expect(context.bindPassword).to.equal('');
    expect(context.form).not.to.have.property('bind_password');
    expect(context.savedAt).to.be.an.instanceof(Date);
    expect(context.saveError).to.equal('');
  });

  it('shows a failed save next to the save button', async () => {
    axios.put = async () => { throw httpError(409, { error: 'LDAP_RECONFIGURATION_REQUIRES_INACTIVE' }); };
    const context = component(LdapProviderSetup);
    context.form = { ...emptyLdapProvider(), id: 'corp' };

    await context.save();

    expect(context.saveError).to.equal('ldapErrorReconfigurationRequiresInactive');
    expect(context.testError).to.equal('');
    expect(context.stateError).to.equal('');
  });

  it('locks connection changes while directory login is exposed', () => {
    const locked = (state) => LdapProviderSetup.computed.configurationLocked.call({
      selectedProvider: { state },
    });
    expect(locked('active')).to.equal(true);
    expect(locked('selected_users')).to.equal(true);
    expect(locked('shadow')).to.equal(false);
  });

  it('tests directory access and local recovery, clears both passwords, then applies the state', async () => {
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
    axios.get = async () => ({ data: [] });
    const context = component(LdapProviderSetup);
    Object.assign(context, {
      selectedProviderID: 'corp',
      providers: [{ id: 'corp', state: 'shadow' }],
      testUsername: 'probe',
      testPassword: 'directory-proof',
      recoveryAdminUserID: 7,
      recoveryAdminPassword: 'local-proof',
      form: {
        ...emptyLdapProvider(), id: 'corp', state: 'selected_users', selected_user_ids: [12],
      },
    });

    await context.testConnection();
    expect(context.testPassword).to.equal('');
    expect(context.recoveryAdminPassword).to.equal('');
    expect(context.readiness.status).to.equal('ready');
    context.providers = [{ id: 'corp', state: 'shadow' }];
    await context.applyState();

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
    expect(context.stateSaved).to.equal(true);
  });

  it('keeps a failed test readiness so the failed stage stays visible', async () => {
    axios.post = async () => {
      throw httpError(503, {
        status: 'failed', code: 'service_bind_failed', detail: 'LDAP Result Code 49 "Invalid Credentials"',
      });
    };
    axios.get = async () => ({ data: [] });
    const context = component(LdapProviderSetup);
    Object.assign(context, {
      selectedProviderID: 'corp',
      testUsername: 'probe',
      testPassword: 'x',
      recoveryAdminUserID: 1,
      recoveryAdminPassword: 'y',
    });

    await context.testConnection();

    expect(context.readiness.code).to.equal('service_bind_failed');
    expect(context.testError).to.equal('');
    expect(context.stageStates.map((stage) => stage.state))
      .to.deep.equal(['failed', 'not_reached', 'not_reached', 'not_reached']);
  });

  it('points to the group mappings when activation needs a mapping', async () => {
    axios.put = async () => { throw httpError(409, { error: 'LDAP_NO_GROUP_MAPPING' }); };
    const context = component(LdapProviderSetup);
    context.selectedProviderID = 'corp';
    context.form = { ...emptyLdapProvider(), id: 'corp', state: 'active' };

    await context.applyState();

    expect(context.stateError).to.equal('ldapErrorNoGroupMapping');
    expect(context.stateErrorNeedsMapping).to.equal(true);
  });

  it('derives a provider ID from the display name until it is edited', () => {
    const context = component(LdapProviderSetup);
    context.displayNameChanged('Example Corp');
    expect(context.form.id).to.equal('example-corp');
    context.providerIdEdited = true;
    context.displayNameChanged('Something else');
    expect(context.form.id).to.equal('example-corp');
  });
});

describe('LDAP setup vocabulary', () => {
  it('fills Active Directory attributes from the preset', () => {
    const form = applyLdapPreset({ ...emptyLdapProvider(), server_url: 'ldaps://dc:636' }, 'active_directory');
    expect(form.server_url).to.equal('ldaps://dc:636');
    expect(form.identity_attribute).to.equal('objectGUID');
    expect(form.username_attribute).to.equal('sAMAccountName');
    expect(form.group_filter).to.equal('(objectClass=group)');
  });

  it('maps failure codes to the stage that stopped the test', () => {
    const states = (code) => ldapStageStates({ status: 'failed', code }).map((stage) => stage.state);
    expect(states('connection_failed')).to.deep.equal(['failed', 'not_reached', 'not_reached', 'not_reached']);
    expect(states('referral_rejected')).to.deep.equal(['passed', 'failed', 'not_reached', 'not_reached']);
    expect(states('invalid_credentials')).to.deep.equal(['passed', 'passed', 'failed', 'not_reached']);
    expect(states('local_recovery_failed')).to.deep.equal(['passed', 'passed', 'passed', 'failed']);
    expect(ldapStageStates({ status: 'ready' }).every((stage) => stage.state === 'passed')).to.equal(true);
    expect(ldapStageStates({ status: 'untested' }).every((stage) => stage.state === 'pending')).to.equal(true);
  });

  it('computes when a successful test stops counting for activation', () => {
    const expiry = readinessExpiry({ status: 'ready', checked_at: '2026-11-14T10:00:00Z' });
    expect(expiry.toISOString()).to.equal('2026-11-14T10:15:00.000Z');
    expect(readinessExpiry({ status: 'failed', checked_at: '2026-11-14T10:00:00Z' })).to.equal(null);
  });

  it('turns API codes into sentences and keeps server sentences as they are', () => {
    expect(ldapErrorText({ response: { data: { error: 'LDAP_INVALID_CREDENTIALS' } } }, t))
      .to.equal('ldapErrorInvalidCredentials');
    expect(ldapErrorText({ response: { data: { error: 'Server URL must use ldaps' } } }, t))
      .to.equal('Server URL must use ldaps');
  });

  it('creates unique readable mapping IDs', () => {
    expect(mappingIdFromName('Ops Team')).to.equal('ops-team');
    expect(mappingIdFromName('Ops Team', ['ops-team'])).to.equal('ops-team-2');
    expect(mappingIdFromName('')).to.equal('group');
  });
});

describe('LDAP group mappings', () => {
  let original;

  beforeEach(() => {
    original = { get: axios.get, post: axios.post, put: axios.put };
  });

  afterEach(() => {
    Object.assign(axios, original);
  });

  it('saves a mapping picked from the directory with its immutable ID and name', async () => {
    let request;
    axios.put = async (url, data) => { request = { url, data }; return { data: {} }; };
    axios.get = async () => ({ data: [] });
    const context = component(LdapGroupMappingPanel);
    context.providerID = 'corp';
    context.editor = {
      id: 'ops',
      group: { external_id: 'entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67', name: 'ops' },
      scope: 'project',
      project_id: 9,
      role_id: 'builtin:task_runner',
      enabled: true,
      expected_revision: 0,
    };
    context.editorOpen = true;

    await context.saveMapping();

    expect(request).to.deep.equal({
      url: '/api/capabilities/ldap/group-mappings/ops',
      data: {
        provider_id: 'corp',
        group_external_id: 'entryuuid:40f1c82a-b773-4d41-a587-7c4cf7f3cd67',
        group_name: 'ops',
        target: { scope: 'project', role_id: 'builtin:task_runner', project_id: 9 },
        enabled: true,
        expected_revision: 0,
      },
    });
    expect(context.editorOpen).to.equal(false);
  });

  it('keeps a failed save inside the mapping dialog', async () => {
    axios.put = async () => { throw httpError(400, { error: 'LDAP group mapping role target does not exist' }); };
    const context = component(LdapGroupMappingPanel);
    context.providerID = 'corp';
    context.editor = {
      id: 'ops', group: { external_id: 'entryuuid:x', name: 'ops' }, scope: 'global', role_id: 'r', enabled: true,
    };
    context.editorOpen = true;

    await context.saveMapping();

    expect(context.editorOpen).to.equal(true);
    expect(context.editorError).to.equal('LDAP group mapping role target does not exist');
    expect(context.listError).to.equal('');
  });

  it('offers built-in project roles for project mappings', async () => {
    const urls = [];
    axios.get = async (url) => { urls.push(url); return { data: [{ id: 'builtin:guest', name: 'Guest' }] }; };
    const context = component(LdapGroupMappingPanel);
    context.editor = { ...context.editor, scope: 'project', project_id: 3 };

    await context.loadProjectRoles();

    expect(urls).to.deep.equal(['/api/project/3/roles/all']);
    expect(context.projectRoles[0].id).to.equal('builtin:guest');
  });

  it('applies a preview that only skips unlinked directory users', () => {
    const canApply = (preview) => LdapGroupMappingPanel.computed.canApply
      .call({ previewResult: preview });
    const base = {
      token: 'one', additions: [], removals: [], skipped: [], unresolved: [], collisions: [], protected_admin_violations: [],
    };
    expect(canApply({ ...base, skipped: [{ kind: 'user', external_id: 'u', reason: 'directory_user_not_linked' }] }))
      .to.equal(true);
    expect(canApply({ ...base, unresolved: [{ kind: 'group', external_id: 'g' }] })).to.equal(false);
    expect(canApply({ ...base, protected_admin_violations: [{ user_id: 1 }] })).to.equal(false);
  });

  it('searches the directory for groups and suggests an ID from the chosen name', async () => {
    let params;
    axios.get = async (url, config) => { params = { url, ...config.params }; return { data: [{ external_id: 'entryuuid:1', name: 'Ops Team', dn: 'cn=ops' }] }; };
    const context = component(LdapGroupMappingPanel);
    context.providerID = 'corp';

    await context.runGroupSearch('ops');
    context.groupChosen(context.groupResults[0]);

    expect(params).to.deep.equal({ url: '/api/capabilities/ldap/groups', provider_id: 'corp', query: 'ops' });
    expect(context.editor.id).to.equal('ops-team');
  });
});

describe('LDAP login messages', () => {
  it('shows explicit provider outage and throttling messages on login', () => {
    expect(Auth.methods.authenticationErrorMessage.call({ $t: (key) => key }, {
      response: { status: 503, data: { error: 'LDAP_PROVIDER_UNAVAILABLE' } },
    })).to.equal('ldapProviderUnavailable');
    expect(Auth.methods.authenticationErrorMessage.call({ $t: (key) => key }, {
      response: { status: 429, data: { error: 'LDAP_THROTTLED' } },
    })).to.equal('ldapLoginThrottled');
  });

  it('explains unsupported legacy LDAP group settings on login', async () => {
    const http = mockAxios();
    http.respond(() => { throw httpError(503, { error: 'LDAP_GROUP_MAPPING_UNSUPPORTED' }); });
    const context = {
      $refs: { signInForm: { validate: async () => ({ valid: true }) } },
      username: 'fixture',
      password: 'secret',
      loginTabs: [{ id: 'corp', ldap: true }],
      activeLoginTab: { id: 'corp', ldap: true },
    };
    try {
      await Auth.methods.signIn.call(context);
      expect(context.signInError).to.contain('configure group mapping through managed LDAP');
      expect(http.requests[0].data.provider).to.equal('corp');
    } finally {
      http.restore();
    }
  });
});
