import { expect } from 'chai';
import { mount } from '@vue/test-utils';
import GlobalSecretStorages from '@/views/GlobalSecretStorages.vue';
import GlobalCredentialReferenceFields, {
  referenceInScope, referencePayload, referenceValid,
} from '@/components/enhanced/GlobalCredentialReferenceFields.vue';
import mockAxios, { httpError } from './helpers/axiosMock';

const Host = { extends: GlobalSecretStorages, methods: { validateForm: async () => true } };
const flush = async () => new Promise((resolve) => { setTimeout(resolve, 0); });
const global = { mocks: { $t: (key) => key }, stubs: { InfisicalPAMStorageFields: true } };

describe('global secret storages', () => {
  let http;
  let wrapper;
  let saveResponse;
  const storage = {
    id: 3,
    revision: 2,
    name: 'Company Infisical',
    type: 'infisical',
    params: { url: 'https://app.infisical.com', client_id: 'client', pam_routes: [] },
    project_ids: [7],
  };

  beforeEach(() => {
    saveResponse = (req) => req.data;
    http = mockAxios();
    http.respond((req) => {
      if (req.method === 'put' || req.method === 'post') return saveResponse(req);
      if (req.url === '/api/projects') return [{ id: 7, name: 'Deploy' }];
      return [JSON.parse(JSON.stringify(storage))];
    });
  });
  afterEach(() => { wrapper?.unmount(); http.restore(); });

  it('lists storages with their granted projects', async () => {
    wrapper = mount(GlobalSecretStorages, { global });
    await flush();
    expect(wrapper.vm.items).to.have.length(1);
    expect(wrapper.vm.projectName(7)).to.equal('Deploy');
  });

  it('sends the revision and the secret source and keeps the dialog open on errors', async () => {
    wrapper = mount(Host, { global });
    await flush();
    wrapper.vm.openEditor(wrapper.vm.items[0]);
    wrapper.vm.secretSource = 'env';
    wrapper.vm.form.secret = 'INFISICAL_CLIENT_SECRET';
    saveResponse = () => { throw httpError(409, { error: 'changed by someone else' }); };

    await wrapper.vm.save();

    const put = http.requests.find((req) => req.method === 'put');
    expect(put.url).to.equal('/api/global-secret-storages/3');
    expect(put.data.revision).to.equal(2);
    expect(put.data.source_storage_type).to.equal('env');
    expect(put.data.secret).to.equal('INFISICAL_CLIENT_SECRET');
    expect(wrapper.vm.editorOpen).to.equal(true);
    expect(wrapper.vm.saveError).to.not.equal(null);
  });

  it('does not prefill the client secret when editing', async () => {
    wrapper = mount(GlobalSecretStorages, { global });
    await flush();
    wrapper.vm.openEditor({ ...wrapper.vm.items[0], secret: 'should-not-appear' });
    expect(wrapper.vm.form.secret).to.equal('');
  });
});

describe('global credential reference fields', () => {
  const providers = [
    { provider: 'openbao', provider_id: 'primary', name: 'primary' },
    { provider: 'infisical', provider_id: 'storage-3', name: 'Company Infisical' },
  ];

  it('switches to Infisical with the current version by default', async () => {
    const wrapper = mount(GlobalCredentialReferenceFields, {
      props: {
        form: {
          provider: 'vault', providerId: '', mount: '', environment: '', path: '', version: 1, field: '',
        },
        providers,
      },
    });
    wrapper.vm.selectSource('infisical|storage-3');
    const [[form]] = wrapper.emitted('update:form');
    expect(form.provider).to.equal('infisical');
    expect(form.providerId).to.equal('storage-3');
    expect(form.version).to.equal(0);
    wrapper.unmount();
  });

  it('validates and builds Infisical references inside the scopes', () => {
    const form = {
      provider: 'infisical', providerId: 'storage-3', mount: 'proj-1', environment: 'prod', path: 'apps/web', version: 0, field: 'DB_PASSWORD',
    };
    const scopes = [{ project_id: 'proj-1', environment: 'prod', path: 'apps' }];
    expect(referenceValid(form, scopes)).to.equal(true);
    expect(referenceValid({ ...form, environment: '' }, scopes)).to.equal(false);
    expect(referenceValid({ ...form, path: 'appsx' }, scopes)).to.equal(false, 'a folder prefix is not a parent');
    expect(referenceValid({ ...form, mount: 'proj-2' }, scopes)).to.equal(false);
    expect(referenceValid(form, [])).to.equal(false, 'a connection without scopes reads nothing');
    expect(referenceInScope({ ...form, path: '' }, [{ project_id: 'proj-1', environment: 'prod', path: '' }])).to.equal(true);
    expect(referencePayload(form)).to.deep.equal({
      provider: 'infisical', provider_id: 'storage-3', mount: 'proj-1', path: 'apps/web', version: 0, field: 'DB_PASSWORD', environment: 'prod',
    });
  });

  it('keeps the Vault rules for Vault references', () => {
    const form = {
      provider: 'vault', providerId: 'primary', mount: 'kv', environment: '', path: 'app', version: 0, field: 'token',
    };
    expect(referenceValid(form)).to.equal(false, 'Vault references pin a version');
    expect(referencePayload({ ...form, version: 2 })).to.not.have.property('environment');
  });
});

describe('GlobalSecretStorages storageBody', () => {
  it('sends only the settings a connection type has', async () => {
    const { storageBody } = await import('@/views/GlobalSecretStorages.vue');
    const body = storageBody({
      id: null,
      name: 'Vault',
      type: 'vault',
      params: {
        url: 'https://vault.example.test', namespace: '', auth_method: 'approle', role_id: 'semaphore', secret_scopes: [{ project_id: 'secret', environment: '', path: 'apps' }],
      },
      secret: 'secret-id',
      project_ids: [3],
    }, 'database');
    expect(body.params).to.deep.equal({
      url: 'https://vault.example.test', auth_method: 'approle', role_id: 'semaphore', secret_scopes: [{ project_id: 'secret', environment: '', path: 'apps' }],
    });
    expect(body.project_ids).to.deep.equal([], 'project assignments belong to Infisical SSH routes');
    expect(body.source_storage_type).to.equal(undefined);
  });

  it('marks a credential read from an environment variable', async () => {
    const { storageBody } = await import('@/views/GlobalSecretStorages.vue');
    const body = storageBody({
      type: 'infisical', params: { url: 'https://app.infisical.com', secret_scopes: [] }, project_ids: [1], secret: 'INFISICAL_SECRET',
    }, 'env');
    expect(body.source_storage_type).to.equal('env');
    expect(body.project_ids).to.deep.equal([1]);
  });
});
