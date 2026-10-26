import './setup';
import { expect } from 'chai';
import axios from 'axios';
import App from '@/App.vue';
import TemplateForm from '@/components/TemplateForm.vue';

describe('application initial API loading', () => {
  let adapter;
  const user = { id: 12 };
  const info = { jwt: { enabled: true, max_ttl: '1h' }, capabilities: {}, global_permissions: {} };
  const host = () => ({
    user: null,
    systemInfo: null,
    unpinnedNavKeys: [],
    applyLanguage(lang) { this.language = lang; },
  });

  beforeEach(() => { adapter = axios.defaults.adapter; });
  afterEach(() => { axios.defaults.adapter = adapter; localStorage.clear(); });

  it('starts all three requests together and restores server preferences from response data', async () => {
    const pending = {};
    axios.defaults.adapter = ({ url }) => new Promise((resolve) => { pending[url] = resolve; });
    const vm = host();
    const loading = App.methods.loadUserInfo.call(vm);
    await new Promise((resolve) => { setTimeout(resolve, 0); });
    expect(Object.keys(pending)).to.have.members(['/api/user', '/api/info', '/api/user/options']);
    pending['/api/info']({ data: info });
    pending['/api/user']({ data: user });
    await new Promise((resolve) => { setTimeout(resolve, 0); });
    expect(vm.user).to.equal(null);
    pending['/api/user/options']({ data: { lang: '"de"', 'nav.unpinnedItems': '["activity"]' } });
    await loading;
    expect(vm.user).to.deep.equal(user);
    expect(vm.systemInfo).to.deep.equal(info);
    expect(vm.language).to.equal('de');
    expect(vm.unpinnedNavKeys).to.deep.equal(['activity']);
  });

  it('preserves defaults for absent options and falls back for malformed preferences', async () => {
    localStorage.setItem('lang', 'en');
    let options = {};
    axios.defaults.adapter = async ({ url }) => ({
      data: { '/api/user': user, '/api/info': info, '/api/user/options': options }[url],
    });
    const vm = host();
    await App.methods.loadUserInfo.call(vm);
    expect(vm.unpinnedNavKeys).to.deep.equal([]);
    expect(vm.language).to.equal(undefined);
    options = { lang: 'invalid', 'nav.unpinnedItems': 'invalid' };
    await App.methods.loadUserInfo.call(vm);
    expect(vm.unpinnedNavKeys).to.deep.equal([]);
    expect(vm.language).to.equal('en');
  });

  ['/api/user', '/api/info', '/api/user/options'].forEach((failedURL) => {
    it(`propagates ${failedURL} failure without publishing partial identity or permissions`, async () => {
      const failure = new Error('request rejected');
      failure.response = { status: 401 };
      axios.defaults.adapter = async ({ url }) => {
        if (url === failedURL) throw failure;
        return { data: { '/api/user': user, '/api/info': info, '/api/user/options': {} }[url] };
      };
      const vm = host();
      let caught;
      try { await App.methods.loadUserInfo.call(vm); } catch (error) { caught = error; }
      expect(caught).to.equal(failure);
      expect(vm.user).to.equal(null);
      expect(vm.systemInfo).to.equal(null);
    });
  });

  it('handles an identity rejection while the options request remains pending', async () => {
    let resolveOptions;
    const failure = new Error('expired session');
    axios.defaults.adapter = ({ url }) => {
      if (url === '/api/user') return Promise.reject(failure);
      if (url === '/api/user/options') return new Promise((resolve) => { resolveOptions = resolve; });
      return Promise.resolve({ data: info });
    };
    const vm = host();
    const outcome = App.methods.loadUserInfo.call(vm).catch((error) => error);
    try {
      expect(await Promise.race([outcome, new Promise((resolve) => { setTimeout(() => resolve('pending'), 50); })])).to.equal(failure);
    } finally {
      resolveOptions({ data: {} });
      await outcome;
    }
    expect(vm.user).to.equal(null);
  });

  it('uses the inherited JWT TTL policy in template forms', () => {
    const vm = { jwt: info.jwt, $t: (key, values) => ({ key, values }) };
    expect(TemplateForm.computed.jwtTtlHint.call(vm)).to.deep.equal({
      key: 'jwt_ttl_hint', values: { max: '1h' },
    });
    vm.jwt = undefined;
    expect(TemplateForm.computed.jwtTtlHint.call(vm).key).to.equal('jwt_ttl_hint_no_max');
  });

  it('normalizes a template without refetching global system information', async () => {
    const requests = [];
    axios.defaults.adapter = async ({ url }) => { requests.push(url); return { data: info }; };
    const vm = {
      item: { type: '', arguments: '[]' },
      schedules: [],
      needField: () => false,
      $set: (target, key, value) => Object.assign(target, { [key]: value }),
      loadRelativeData: async () => {},
    };
    await TemplateForm.methods.afterLoadData.call(vm);
    expect(requests).to.deep.equal([]);
    expect(vm.item.jwt_params).to.deep.equal({ enabled: false, audience: [], ttl: '' });
    expect(vm.item.runner_tags).to.deep.equal([]);
    expect(vm.item.executor_image).to.equal(null);
  });
});
