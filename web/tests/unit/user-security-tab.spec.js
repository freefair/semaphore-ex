import './setup';
import { expect } from 'chai';
import { mount } from '@vue/test-utils';
import Vuetify from 'vuetify';
import UserForm from '@/components/UserForm.vue';
import mockAxios from './helpers/axiosMock';

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
      vuetify: new Vuetify(),
      attachTo: document.body,
      data: () => ({ item: { id: 12, username: 'fixture', external: false } }),
      propsData: {
        itemId: 12,
        isSelf: true,
        isAdmin: true,
        authMethods: { totp: true },
        LoginWithPassword: true,
      },
      mocks: { $t: (key) => key },
      stubs: { TotpEnrollmentPanel: { template: '<div data-testid="totp-panel">TOTP</div>' } },
    });
    try {
      await flush();
      await wrapper.setData({ tab: 'security' });
      await flush();
      expect(wrapper.find('[data-testid="totp-panel"]').exists()).to.equal(true);
      expect(wrapper.find('[data-testid="totp-panel"]').isVisible()).to.equal(true);
      expect(wrapper.text()).to.contain('Change password');
    } finally {
      wrapper.destroy();
      http.restore();
    }
  });
});
