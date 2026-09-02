import { expect } from 'chai';
import axios from 'axios';
import './local-storage-fixture';
import GlobalCredentials from '@/views/GlobalCredentials.vue';
import GrantedCredentials from '@/views/project/GrantedCredentials.vue';
import SecretStorages from '@/views/project/SecretStorages.vue';
import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';

describe('Global credential administration', () => {
  let originalGet;
  let originalPost;
  let originalDelete;

  beforeEach(() => {
    originalGet = axios.get;
    originalPost = axios.post;
    originalDelete = axios.delete;
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.post = originalPost;
    axios.delete = originalDelete;
  });

  it('keeps administration and project permissions independent', () => {
    expect(GLOBAL_PERMISSIONS.manageCredentialMetadata).to.equal(16);
    expect(GLOBAL_PERMISSIONS.rotateCredentials).to.equal(32);
    expect(GLOBAL_PERMISSIONS.grantCredentials).to.equal(64);
    expect(USER_PERMISSIONS.listGrantedCredentials).to.equal(1024);
    expect(USER_PERMISSIONS.consumeGrantedCredentials).to.equal(2048);
  });

  it('submits local material write-only and clears it after success', async () => {
    let request;
    axios.post = async (url, body) => {
      request = { url, body };
      return { data: { id: 7 } };
    };
    const context = {
      createForm: GlobalCredentials.data().createForm,
      materialPayload: GlobalCredentials.methods.materialPayload,
      closeCreate: GlobalCredentials.methods.closeCreate,
      load: async () => {},
      notifyError: () => { throw new Error('unexpected failure'); },
    };
    context.createForm.displayName = 'Registry';
    context.createForm.stringValue = 'must-not-remain';

    await GlobalCredentials.methods.createCredential.call(context);

    expect(request).to.deep.equal({
      url: '/api/global-credentials',
      body: {
        type: 'string',
        display_name: 'Registry',
        material: { string_value: 'must-not-remain' },
      },
    });
    expect(context.createForm.stringValue).to.equal('');
  });

  it('clears rotation material after a failed request', async () => {
    axios.post = async () => { throw new Error('failed'); };
    const context = {
      rotateForm: {
        id: 7,
        revision: 2,
        materialKind: 'local_encrypted',
        stringValue: 'must-not-remain',
      },
      materialPayload: GlobalCredentials.methods.materialPayload,
      closeRotate: GlobalCredentials.methods.closeRotate,
      load: async () => {},
      notifyError: () => {},
    };

    await GlobalCredentials.methods.rotateCredential.call(context);

    expect(context.rotateForm.stringValue).to.equal('');
  });

  it('serializes external references without a local value', () => {
    const result = GlobalCredentials.methods.materialPayload({
      materialKind: 'external_reference',
      provider: 'openbao',
      providerId: 'primary',
      mount: 'kv',
      path: 'team/registry',
      version: '3',
      field: 'token',
      stringValue: 'ignored',
    });

    expect(result).to.deep.equal({
      external_reference: {
        provider: 'openbao',
        provider_id: 'primary',
        mount: 'kv',
        path: 'team/registry',
        version: 3,
        field: 'token',
      },
    });
  });

  it('uses the dedicated selector endpoint when managing grants', async () => {
    const calls = [];
    axios.get = async (url) => {
      calls.push(url);
      return { data: [] };
    };
    const context = {
      grantCredential: { id: 9 },
      grants: [],
      grantProjects: [],
      notifyError: () => { throw new Error('unexpected failure'); },
    };

    await GlobalCredentials.methods.loadGrants.call(context);

    expect(calls).to.have.members([
      '/api/global-credentials/9/grants?count=100&offset=0',
      '/api/global-credentials/grant-projects',
    ]);
  });

  it('uses revisions for revoke and delete operations', async () => {
    const requests = [];
    axios.post = async (url, body) => { requests.push({ method: 'post', url, body }); };
    axios.delete = async (url) => { requests.push({ method: 'delete', url }); };
    const context = {
      grantCredential: { id: 9 },
      loadGrants: async () => {},
      notifyError: () => { throw new Error('unexpected failure'); },
    };

    await GlobalCredentials.methods.setGrantStatus.call(context, { id: 4, revision: 3 }, 'revoke');
    await GlobalCredentials.methods.deleteGrant.call(context, { id: 4, revision: 4 });

    expect(requests).to.deep.equal([
      {
        method: 'post',
        url: '/api/global-credentials/9/grants/4/revoke',
        body: { revision: 3 },
      },
      {
        method: 'delete',
        url: '/api/global-credentials/9/grants/4?expected_revision=4',
      },
    ]);
  });
});

describe('Project granted credential selection', () => {
  let originalGet;

  beforeEach(() => { originalGet = axios.get; });
  afterEach(() => { axios.get = originalGet; });

  it('loads only project-scoped safe metadata and selects by stable ID', async () => {
    const calls = [];
    axios.get = async (url) => {
      calls.push(url);
      return { data: [{ credential_id: 11, display_name: 'Registry', operations: 3 }] };
    };
    const context = {
      projectId: 5,
      allowed: true,
      loading: false,
      items: [],
      selectedId: null,
      notifyError: () => { throw new Error('unexpected failure'); },
    };

    await GrantedCredentials.methods.load.call(context);
    context.selectedId = 11;

    expect(calls).to.deep.equal(['/api/project/5/granted-credentials?count=100&offset=0']);
    expect(context.items[0]).not.to.have.any.keys('fingerprint', 'owner_user_id', 'external_reference', 'string_value', 'encrypted_material');
    expect(context.selectedId).to.equal(11);
  });

  it('does not call the API without the list permission', async () => {
    let called = false;
    axios.get = async () => { called = true; return { data: [] }; };

    await GrantedCredentials.methods.load.call({ allowed: false });

    expect(called).to.equal(false);
  });

  it('keeps the Granted tab out of Community builds', () => {
    expect(SecretStorages.props.isPro.default).to.equal(false);
  });
});
