import './setup';
import { expect } from 'chai';
import { mount } from '@vue/test-utils';

import UserForm from '@/components/UserForm.vue';
import mockAxios, { httpError } from './helpers/axiosMock';

const flush = () => new Promise((resolve) => { setTimeout(resolve, 0); });

describe('UserForm named Security tab', () => {
  let restoreStorage;

  beforeEach(() => {
    const values = new Map();
    const storage = {
      getItem: (key) => (values.has(key) ? values.get(key) : null),
      setItem: (key, value) => values.set(key, String(value)),
      removeItem: (key) => values.delete(key),
      clear: () => values.clear(),
    };
    const previous = [global, window].map((target) => ({
      target,
      descriptor: Object.getOwnPropertyDescriptor(target, 'localStorage'),
    }));
    previous.forEach(({ target }) => {
      Object.defineProperty(target, 'localStorage', {
        configurable: true, writable: true, value: storage,
      });
    });
    restoreStorage = () => previous.forEach(({ target, descriptor }) => {
      if (descriptor) Object.defineProperty(target, 'localStorage', descriptor);
      else Reflect.deleteProperty(target, 'localStorage');
    });
  });

  afterEach(() => restoreStorage());

  it('renders password and TOTP controls in the selected Security panel', async () => {
    const http = mockAxios();
    http.respond(({ url }) => {
      if (url === '/api/users/12') return { id: 12, username: 'fixture', external: false };
      if (url === '/api/auth/login') return { oidc_providers: [], ldap_providers: [] };
      return [];
    });
    const wrapper = mount(UserForm, {

      attachTo: document.body,
      data: () => ({ item: { id: 12, username: 'fixture', external: false } }),
      props: {
        itemId: 12,
        isSelf: true,
        isAdmin: true,
        authMethods: { totp: true },
        loginWithPassword: true,
      },
      global: {
        renderStubDefaultSlot: true,
        mocks: { $t: (key) => key },
        stubs: { TotpEnrollmentPanel: { template: '<div data-testid="totp-panel">TOTP</div>' } },
      },
    });
    try {
      await flush();
      await wrapper.setData({ tab: 'security' });
      await flush();
      expect(wrapper.find('[data-testid="totp-panel"]').exists()).to.equal(true);
      expect(wrapper.find('[data-testid="totp-panel"]').isVisible()).to.equal(true);
      expect(wrapper.text()).to.contain('Change password');
    } finally {
      wrapper.unmount();
      http.restore();
    }
  });

  it('explains unsupported legacy LDAP group settings when linking', async () => {
    const http = mockAxios();
    http.respond(({ url, method }) => {
      if (url === '/api/users/12') return { id: 12, username: 'fixture', external: false };
      if (url === '/api/auth/login') return { oidc_providers: [], ldap_providers: [{ id: 'corp', name: 'Corp' }] };
      if (method === 'post' && url === '/api/user/identities/ldap') {
        throw httpError(503, { error: 'LDAP_GROUP_MAPPING_UNSUPPORTED' });
      }
      return [];
    });
    const wrapper = mount(UserForm, {
      props: {
        itemId: 12, isSelf: true, authMethods: {}, loginWithPassword: true,
      },
      global: { mocks: { $t: (key) => key } },
    });
    try {
      await flush();
      await wrapper.setData({ tab: 'security', ldapUsername: 'fixture', ldapPassword: 'secret' });
      await wrapper.vm.linkLdapIdentity();
      await flush();
      expect(wrapper.find('[data-testid="user-linkError"]').text()).to.contain('configure group mapping through managed LDAP');
    } finally {
      wrapper.unmount();
      http.restore();
    }
  });
});
