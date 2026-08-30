import { expect } from 'chai';
import fs from 'fs';
import path from 'path';
import axios from 'axios';
import * as compiler from 'vue-template-compiler';
import OidcGroupMappingPanel from '@/components/OidcGroupMappingPanel.vue';

describe('OIDC group mapping UI contracts', () => {
  let originalGet;
  let originalPost;
  let originalPut;
  let originalDelete;

  beforeEach(() => {
    originalGet = axios.get;
    originalPost = axios.post;
    originalPut = axios.put;
    originalDelete = axios.delete;
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.post = originalPost;
    axios.put = originalPut;
    axios.delete = originalDelete;
  });

  it('compiles the panel inside the existing system information surface', () => {
    const filename = path.resolve(process.cwd(), 'src/components/OidcGroupMappingPanel.vue');
    const descriptor = compiler.parseComponent(fs.readFileSync(filename, 'utf8'));
    const result = compiler.compile(descriptor.template.content);

    expect(result.errors).to.deep.equal([]);
    expect(descriptor.template.content).to.include('data-testid="oidc-group-mapping"');
    expect(descriptor.template.content).to.include('oidc-responsive-table');
    expect(descriptor.template.content).to.include('d-flex align-center flex-wrap mb-4');
  });

  it('loads only the secret-free provider projection and existing state', async () => {
    const requests = [];
    axios.get = async (url) => {
      requests.push(url);
      if (url.endsWith('/providers')) {
        return {
          data: [{
            id: 'corp',
            display_name: 'Corporate',
            claim_configuration: {
              path: 'realm.groups', case_insensitive: false, missing_claim_policy: 'preserve',
            },
          }],
        };
      }
      if (url.endsWith('/history')) return { data: [{ id: 1, status: 'applied' }] };
      if (url.endsWith('/assignments')) return { data: [{ user_id: 9 }] };
      return { data: [{ id: 'engineering' }] };
    };
    const context = {
      available: false,
      providers: [],
      providerId: '',
      mappings: [],
      history: [],
      assignments: [],
      preview: null,
      error: null,
      provider: null,
      resetMappingForm() {},
      errorMessage: OidcGroupMappingPanel.methods.errorMessage,
      async loadProviderState() {
        this.provider = this.providers[0];
        return OidcGroupMappingPanel.methods.loadProviderState.call(this);
      },
    };

    await OidcGroupMappingPanel.methods.loadProviders.call(context);

    expect(context.available).to.equal(true);
    expect(context.providerId).to.equal('corp');
    expect(context.mappings).to.deep.equal([{ id: 'engineering' }]);
    expect(context.history).to.have.length(1);
    expect(context.assignments).to.have.length(1);
    expect(JSON.stringify(context.providers)).not.to.include('client_secret');
    expect(requests).to.include('/api/capabilities/oidc/group-mappings/assignments');
  });

  it('keeps the enhanced panel absent when the capability endpoint is unavailable', async () => {
    axios.get = async () => {
      const error = new Error('not found');
      error.response = { status: 404 };
      throw error;
    };
    const context = {
      available: false,
      providers: [],
      providerId: '',
      error: null,
      errorMessage: OidcGroupMappingPanel.methods.errorMessage,
    };

    await OidcGroupMappingPanel.methods.loadProviders.call(context);

    expect(context.available).to.equal(false);
    expect(context.error).to.equal(null);
  });

  it('saves an explicit role target with optimistic revision', async () => {
    let request;
    axios.put = async (url, data) => {
      request = { url, data };
      return { data: { id: 'engineering' } };
    };
    const context = {
      providerId: 'corp',
      mappingForm: {
        id: 'engineering',
        claim_value: 'Engineering',
        enabled: true,
        expected_revision: 3,
        target: { scope: 'project', project_id: 12, role_id: 'runner' },
      },
      saving: false,
      error: null,
      errorMessage: OidcGroupMappingPanel.methods.errorMessage,
      async loadProviderState() { return undefined; },
    };

    await OidcGroupMappingPanel.methods.saveMapping.call(context);

    expect(request).to.deep.equal({
      url: '/api/capabilities/oidc/group-mappings/engineering',
      data: {
        provider_id: 'corp',
        claim_value: 'Engineering',
        target: { scope: 'project', role_id: 'runner', project_id: 12 },
        enabled: true,
        expected_revision: 3,
      },
    });
    expect(context.saving).to.equal(false);
  });

  it('previews only newline-delimited group values without token or claim document fields', async () => {
    let request;
    axios.post = async (url, data) => {
      request = { url, data };
      return { data: { additions: [{ mapping_id: 'engineering' }], unknown_values: ['unknown'] } };
    };
    const context = {
      providerId: 'corp',
      previewUserId: 9,
      previewClaimValues: 'engineering\nunknown\n',
      previewing: false,
      error: null,
      preview: null,
      normalizedPreview: OidcGroupMappingPanel.methods.normalizedPreview,
      errorMessage: OidcGroupMappingPanel.methods.errorMessage,
    };

    await OidcGroupMappingPanel.methods.previewMappings.call(context);

    expect(request).to.deep.equal({
      url: '/api/capabilities/oidc/group-mappings/preview',
      data: { provider_id: 'corp', user_id: 9, claim: ['engineering', 'unknown'] },
    });
    expect(JSON.stringify(request.data)).not.to.include('token');
    expect(context.preview.additions).to.have.length(1);
    expect(context.preview.removals).to.deep.equal([]);
    expect(context.preview.protected_admin_violations).to.deep.equal([]);
  });

  it('shows stale mapping conflicts from the API', () => {
    const message = OidcGroupMappingPanel.methods.errorMessage({
      response: { data: { error: 'OIDC_GROUP_MAPPING_STALE' } },
    });
    expect(message).to.equal('OIDC_GROUP_MAPPING_STALE');
  });
});
