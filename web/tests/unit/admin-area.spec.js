import { expect } from 'chai';
import axios from 'axios';
import router from '@/router';
import {
  ADMIN_SECTIONS, LEGACY_ADMIN_REDIRECTS, adminAccess, visibleAdminSections,
} from '@/lib/enhanced/admin-sections';
import { environmentVariable, notificationChannelStates } from '@/lib/enhanced/notification-channels';
import TwoFactorPolicyPanel from '@/components/enhanced/TwoFactorPolicyPanel.vue';

const NONE = {
  admin: false,
  manageUsers: false,
  manageRoles: false,
  manageSystem: false,
  readAuditLog: false,
  governance: false,
  credentials: false,
};

describe('administration area', () => {
  it('shows an administrator every section', () => {
    const all = Object.fromEntries(Object.keys(NONE).map((key) => [key, true]));
    expect(visibleAdminSections(all)).to.have.length(ADMIN_SECTIONS.length);
  });

  it('shows only the sections a delegated permission allows', () => {
    const keys = visibleAdminSections({ ...NONE, manageUsers: true, readAuditLog: true })
      .map((section) => section.key);
    expect(keys).to.deep.equal(['users', 'audit-log']);
  });

  it('shows no section without any administration permission', () => {
    expect(visibleAdminSections(NONE)).to.deep.equal([]);
  });

  it('derives access from the user and the computed global permissions', () => {
    const access = adminAccess({
      user: { admin: false, can_read_audit_log: true },
      canManageGlobalUsers: true,
      canManageGlobalRoles: false,
      canManageGlobalSystem: false,
      canAccessGlobalGovernance: false,
      canAccessGlobalCredentials: true,
    });
    expect(access).to.deep.equal({
      ...NONE, manageUsers: true, readAuditLog: true, credentials: true,
    });
  });

  it('routes every section and redirects the old addresses', () => {
    ADMIN_SECTIONS.forEach((section) => {
      const resolved = router.resolve(section.path);
      expect(resolved.matched.length, section.path).to.equal(2);
    });
    Object.entries(LEGACY_ADMIN_REDIRECTS).forEach(([legacy, target]) => {
      const record = router.getRoutes().find((route) => route.path === legacy);
      expect(record, legacy).to.not.equal(undefined);
      expect(record.redirect).to.equal(target);
    });
    expect(LEGACY_ADMIN_REDIRECTS['/role-mappings']).to.equal('/admin/group-mappings');
  });
});

describe('notification channel overview', () => {
  it('marks configured channels and names the environment variables', () => {
    const states = notificationChannelStates({ email: true, slack: false });
    expect(states.find((channel) => channel.key === 'email').configured).to.equal(true);
    expect(states.find((channel) => channel.key === 'slack').configured).to.equal(false);
    expect(states.find((channel) => channel.key === 'gotify').configured).to.equal(false);
    expect(environmentVariable('email_host')).to.equal('SEMAPHORE_EMAIL_HOST');
  });
});

describe('two-factor policy panel', () => {
  function panelContext() {
    const context = {
      ...TwoFactorPolicyPanel.data(),
      $t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
    };
    Object.entries(TwoFactorPolicyPanel.methods).forEach(([name, method]) => {
      context[name] = method.bind(context);
    });
    return context;
  }

  it('loads the policy, applies it and confirms the new state', async () => {
    const previous = { get: axios.get, put: axios.put };
    axios.get = async (url) => ({
      data: {
        '/api/capabilities/totp': { state: 'required_selected', selected_user_ids: [4] },
        '/api/users': [{ id: 4, username: 'local', external: false }, { id: 5, username: 'ldap', external: true }],
        '/api/capabilities/totp/transitions': [{
          id: 1, from_state: 'disabled', to_state: 'required_selected', actor_id: 4, created: '2026-11-14T10:00:00Z',
        }],
      }[url],
    });
    let saved;
    axios.put = async (url, data) => { saved = { url, data }; };
    try {
      const context = panelContext();
      await context.loadTotpRollout();
      expect(context.totpRollout.selected_user_ids).to.deep.equal([4]);
      expect(context.actorName(4)).to.equal('local');

      await context.saveTotpRollout();
      expect(saved.url).to.equal('/api/capabilities/totp');
      expect(saved.data.state).to.equal('required_selected');
      expect(context.saved).to.equal(true);
      expect(context.totpRolloutError).to.equal(null);
    } finally {
      axios.get = previous.get;
      axios.put = previous.put;
    }
  });

  it('explains the administrator recovery requirement instead of a raw code', () => {
    const context = panelContext();
    expect(context.errorText('TOTP_ADMIN_RECOVERY_NOT_READY')).to.equal('twoFactorErrorAdminRecovery');
    expect(context.errorAction('TOTP_ADMIN_RECOVERY_NOT_READY'))
      .to.equal('twoFactorErrorAdminRecoveryAction');
    expect(context.errorText('SOMETHING_ELSE')).to.equal('SOMETHING_ELSE');
  });

  it('labels every policy state with a description', () => {
    const context = panelContext();
    context.states.forEach((state) => {
      expect(state.description).to.match(/Hint$/);
      expect(context.stateLabel(state.value)).to.equal(state.label);
    });
  });
});
