import { expect } from 'chai';
import axios from 'axios';
import './local-storage-fixture';
import PolicyGuardrailsPanel from '@/components/PolicyGuardrailsPanel.vue';
import Settings from '@/views/project/Settings.vue';
import AuditWebhooks from '@/views/AuditWebhooks.vue';
import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';

describe('policy guardrail governance UI', () => {
  let originalGet;
  let originalPut;
  let originalPost;

  beforeEach(() => {
    originalGet = axios.get;
    originalPut = axios.put;
    originalPost = axios.post;
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.put = originalPut;
    axios.post = originalPost;
  });

  it('uses the dedicated independent project and global permissions', () => {
    expect(USER_PERMISSIONS.overrideDeploymentWindow).to.equal(4096);
    expect(USER_PERMISSIONS.managePolicyGuardrails).to.equal(8192);
    expect(USER_PERMISSIONS.rollbackPolicyGuardrails).to.equal(16384);
    expect(GLOBAL_PERMISSIONS.managePolicyGuardrails).to.equal(128);
    expect(GLOBAL_PERMISSIONS.rollbackPolicyGuardrails).to.equal(256);
  });

  it('keeps the panel absent without capability and resolves scope from props only', () => {
    expect(Settings.computed.policyGuardrailsDecision.call({ systemInfo: {} })).to.equal(null);
    const decision = { id: 'policy_guardrails', access: ['read', 'write', 'execute'] };
    expect(Settings.computed.policyGuardrailsDecision.call({
      systemInfo: { capabilities: { capabilities: [decision] } },
    })).to.equal(decision);
    expect(PolicyGuardrailsPanel.computed.baseUrl.call({ projectId: 7 }))
      .to.equal('/api/project/7/policy-guardrails');
    expect(PolicyGuardrailsPanel.computed.baseUrl.call({ projectId: null }))
      .to.equal('/api/policy-guardrails');
  });

  it('opens the existing global governance surface for policy-only roles', () => {
    const systemInfo = {
      global_permissions: { permissions: GLOBAL_PERMISSIONS.managePolicyGuardrails },
    };
    const context = { systemInfo, isAdmin: false };
    expect(AuditWebhooks.computed.showAuditGovernance.call(context)).to.equal(false);
    expect(AuditWebhooks.computed.canManagePolicyGuardrails.call(context)).to.equal(true);
    expect(AuditWebhooks.computed.canRollbackPolicyGuardrails.call(context)).to.equal(false);
  });

  it('loads bounded governance state from the selected scope', async () => {
    const requests = [];
    axios.get = async (url, config) => {
      requests.push({ url, config });
      if (url.endsWith('/revisions')) {
        return { data: [{ id: 2, revision: 2 }, { id: 1, revision: 1 }] };
      }
      if (url.endsWith('/evaluations')) return { data: [{ id: 4, decision: 'allow' }] };
      return { data: { draft: { revision: 3, source_yaml: 'version: 1\nrules: []\n' }, active: { revision: 2 } } };
    };
    const context = {
      canManage: true,
      baseUrl: '/api/project/7/policy-guardrails',
      loading: false,
      error: '',
      state: null,
      sourceYaml: '',
      revisions: [],
      evaluations: [],
      rollbackExpectedRevision: null,
      rollbackRevision: null,
      diffFrom: null,
      diffTo: null,
      selectRevisionDefaults: PolicyGuardrailsPanel.methods.selectRevisionDefaults,
    };

    await PolicyGuardrailsPanel.methods.load.call(context);

    expect(context.sourceYaml).to.equal('version: 1\nrules: []\n');
    expect(context.rollbackExpectedRevision).to.equal(3);
    expect(context.diffFrom).to.equal(1);
    expect(context.diffTo).to.equal(2);
    expect(requests.map(({ url }) => url)).to.deep.equal([
      '/api/project/7/policy-guardrails',
      '/api/project/7/policy-guardrails/revisions',
      '/api/project/7/policy-guardrails/evaluations',
    ]);
    expect(requests[1].config.params.count).to.equal(25);
  });

  it('sends only source, revisions, fixture metadata, and rollback reason', async () => {
    const requests = [];
    axios.put = async (url, body) => { requests.push({ method: 'put', url, body }); return { data: { revision: 4 } }; };
    axios.post = async (url, body) => { requests.push({ method: 'post', url, body }); return { data: { revision: 5 } }; };
    const context = {
      baseUrl: '/api/policy-guardrails',
      sourceYaml: 'version: 1\nrules: []\n',
      state: { draft: { revision: 3 } },
      rollbackExpectedRevision: 3,
      rollbackRevision: 2,
      rollbackReason: 'restore known-safe revision',
      fixtureJson: JSON.stringify({
        project_id: 7,
        intent: 'task',
        evaluated_at: '2026-09-03T10:00:00Z',
        template: { id: 1, application: 'ansible', source: 'manual' },
        executor: { type: 'unknown', image_reference_kind: 'none' },
      }),
      action: '',
      error: '',
      perform: PolicyGuardrailsPanel.methods.perform,
      parseFixture: PolicyGuardrailsPanel.methods.parseFixture,
      load: async () => {},
      $t: (key) => key,
    };
    Object.defineProperty(context, 'expectedRollbackRevision', { get: () => context.state.draft.revision });

    await PolicyGuardrailsPanel.methods.saveDraft.call(context);
    await PolicyGuardrailsPanel.methods.testFixture.call(context);
    await PolicyGuardrailsPanel.methods.previewImpact.call(context);
    await PolicyGuardrailsPanel.methods.publishDraft.call(context);
    await PolicyGuardrailsPanel.methods.rollback.call(context);

    expect(requests[0]).to.deep.equal({
      method: 'put',
      url: '/api/policy-guardrails/draft',
      body: { source_yaml: 'version: 1\nrules: []\n', expected_revision: 3 },
    });
    expect(requests[1].url).to.equal('/api/policy-guardrails/test');
    expect(requests[1].body).to.have.keys(['source_yaml', 'input']);
    expect(requests[2]).to.deep.include({ method: 'post', url: '/api/policy-guardrails/impact' });
    expect(requests[2].body.inputs).to.have.length(1);
    expect(requests[3].body).to.deep.equal({ expected_draft_revision: 4 });
    expect(requests[4].body).to.deep.equal({
      revision: 2,
      expected_draft_revision: 4,
      reason: 'restore known-safe revision',
    });
    expect(JSON.stringify(requests)).not.to.include('secret_value');
  });
});
