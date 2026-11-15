import { expect } from 'chai';
import { h } from 'vue';
import { mount } from '@vue/test-utils';
import EnvironmentForm from '@/components/EnvironmentForm.vue';
import mockAxios, { httpError } from './helpers/axiosMock';

const FormStub = {
  render: () => h('form'),
  methods: { validate: async () => ({ valid: true }), resetValidation: () => {} },
};
const Host = {
  extends: EnvironmentForm,
  components: { FormStub },
  render() { return h(FormStub, { ref: 'form' }); },
  methods: { $t: (key) => key },
};
const flush = async () => new Promise((resolve) => { setTimeout(resolve, 0); });

describe('variable group global credential references', () => {
  let http;
  let wrapper;
  let granted;
  const group = {
    id: 1,
    name: 'Deploy',
    json: '{}',
    env: '{}',
    credentials: [{ name: 'TF_VAR_license', target: 'env', credential_id: 4 }],
  };

  beforeEach(() => {
    granted = () => [
      { credential_id: 4, display_name: 'License server', operations: 3 },
      { credential_id: 5, display_name: 'Consume only', operations: 2 },
      { credential_id: 6, display_name: 'Reference only', operations: 1 },
    ];
    http = mockAxios();
    http.respond((req) => {
      if (['put', 'post'].includes(req.method)) return req.data;
      if (req.url.includes('/granted-credentials')) return granted();
      if (req.url.endsWith('/secret_storages')) return [];
      return JSON.parse(JSON.stringify(group));
    });
  });
  afterEach(() => { wrapper?.unmount(); http.restore(); });

  it('offers only credentials the project may reference and saves the references', async () => {
    wrapper = mount(Host, { props: { projectId: 7, itemId: 1 } });
    await flush();

    expect(wrapper.vm.credentials).to.deep.equal(group.credentials);
    expect(wrapper.vm.credentialChoices(wrapper.vm.credentials[0])).to.deep.equal([
      { title: 'License server', value: 4 },
    ]);

    wrapper.vm.addCredential();
    Object.assign(wrapper.vm.credentials[1], { name: 'license_key', target: 'var', credential_id: 4 });
    await wrapper.vm.save();

    const saved = http.requests.find((req) => req.method === 'put');
    expect(saved.data.credentials).to.deep.equal([
      { name: 'TF_VAR_license', target: 'env', credential_id: 4 },
      { name: 'license_key', target: 'var', credential_id: 4 },
    ]);
  });

  it('reads every page of granted credentials', async () => {
    const page = (offset, size) => Array.from({ length: size }, (_, index) => ({
      credential_id: offset + index + 10, display_name: `Credential ${offset + index}`, operations: 3,
    }));
    granted = () => [];
    http.respond((req) => {
      if (['put', 'post'].includes(req.method)) return req.data;
      if (req.url.includes('/granted-credentials')) {
        const offset = Number(new URL(req.url, 'http://x').searchParams.get('offset'));
        return offset === 0 ? page(0, 100) : page(100, 5);
      }
      if (req.url.endsWith('/secret_storages')) return [];
      return JSON.parse(JSON.stringify(group));
    });
    wrapper = mount(Host, { props: { projectId: 7, itemId: 1 } });
    await flush();
    await flush();

    expect(wrapper.vm.grantedCredentials).to.have.length(105);
  });

  it('keeps a reference whose grant ended visible', async () => {
    granted = () => [];
    wrapper = mount(Host, { props: { projectId: 7, itemId: 1 } });
    await flush();

    const [choice] = wrapper.vm.credentialChoices(wrapper.vm.credentials[0]);
    expect(choice.value).to.equal(4);
  });

  it('shows the references read-only when the role may not list credentials', async () => {
    granted = () => { throw httpError(403, {}); };
    wrapper = mount(Host, { props: { projectId: 7, itemId: 1 } });
    await flush();

    expect(wrapper.vm.formError).to.equal(null);
    expect(wrapper.vm.grantedCredentials).to.equal(null);
    expect(wrapper.vm.credentials).to.have.length(1);
  });

  it('validates names per target', async () => {
    wrapper = mount(Host, { props: { projectId: 7, itemId: 1 } });
    await flush();
    const rule = (target) => wrapper.vm.credentialNameRule({ target });

    expect(rule('env')('TF_VAR_token')).to.equal(true);
    expect(rule('env')('1TOKEN')).to.not.equal(true);
    expect(rule('var')('license_key')).to.equal(true);
    expect(rule('var')('LicenseKey')).to.not.equal(true);
    expect(rule('env')('TF_VAR_license')).to.equal(true);
    wrapper.vm.addCredential();
    wrapper.vm.credentials[1].name = 'TF_VAR_license';
    expect(rule('env')('TF_VAR_license')).to.not.equal(true);
  });
});
