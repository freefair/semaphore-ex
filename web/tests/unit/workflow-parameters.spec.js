import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import WorkflowNodeOverridePolicyEditor from '@/components/WorkflowNodeOverridePolicyEditor.vue';
import WorkflowParameterAudit from '@/components/WorkflowParameterAudit.vue';
import WorkflowParameterEditor from '@/components/WorkflowParameterEditor.vue';
import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';

describe('workflow parameters and node overrides', () => {
  let originalGet;

  beforeEach(() => {
    originalGet = axios.get;
    axios.get = async () => ({ data: [] });
  });

  afterEach(() => {
    axios.get = originalGet;
  });

  it('creates every supported declaration shape without changing the graph model', () => {
    expect(WorkflowParameterEditor.methods.newParameter('string')).to.include({ type: 'string' });
    expect(WorkflowParameterEditor.methods.newParameter('integer')).to.include({ type: 'integer' });
    expect(WorkflowParameterEditor.methods.newParameter('boolean')).to.include({ type: 'boolean' });
    expect(WorkflowParameterEditor.methods.newParameter('enumeration')).to.deep.include({
      type: 'enumeration', options: [],
    });
    expect(WorkflowParameterEditor.methods.newParameter('secret_reference')).to.deep.include({
      type: 'secret_reference', secret_options: [],
    });
  });

  it('exposes the same string length ceiling as the backend contract', () => {
    expect(WorkflowParameterEditor.data().maxStringBytes).to.equal(4096);
  });

  it('normalizes one node policy into a single backend allow-list', () => {
    expect(WorkflowNodeOverridePolicyEditor.methods.preparePolicy({
      inventory_ids: [11], allow_arguments: true,
    })).to.deep.equal({
      inventory_ids: [11],
      environment_ids: [],
      credential_parameters: [],
      allow_arguments: true,
      allow_branch: false,
    });
  });

  it('builds a value-safe run request with allow-listed node fields only', () => {
    const context = {
      values: { region: 'eu', token: 41 },
      nodeValues: { 7: { inventory_id: 5, arguments: '["--check"]', ignored: 'no' } },
      workflow: {
        parameters: [
          { name: 'region', type: 'string' },
          { name: 'token', type: 'secret_reference' },
        ],
        nodes: [{ id: 7, override_policy: { inventory_ids: [5], allow_arguments: true } }],
      },
    };

    const payload = WorkflowRunDialog.methods.buildPayload.call(context);

    expect(payload).to.deep.equal({
      parameters: { region: 'eu', token: { access_key_id: 41 } },
      node_overrides: { 7: { inventory_id: 5, arguments: '["--check"]' } },
    });
    expect(JSON.stringify(payload)).not.to.contain('ignored');
  });

  it('drops fields that are outside the node policy even when present in local state', () => {
    const payload = WorkflowRunDialog.methods.buildPayload.call({
      values: {},
      nodeValues: { 7: { inventory_id: 9, environment_ids: [4], git_branch: 'main' } },
      workflow: {
        parameters: [],
        nodes: [{ id: 7, override_policy: { inventory_ids: [5], environment_ids: [3] } }],
      },
    });

    expect(payload).to.deep.equal({});
  });

  it('can explicitly override a non-empty string default with an empty string', () => {
    const payload = WorkflowRunDialog.methods.buildPayload.call({
      values: { note: '' },
      nodeValues: {},
      workflow: {
        parameters: [{ name: 'note', type: 'string', default: 'from-definition' }],
        nodes: [],
      },
    });

    expect(payload).to.deep.equal({ parameters: { note: '' } });
  });

  it('accepts an explicit empty required string when its bounds allow it', () => {
    const errors = WorkflowRunDialog.computed.errors.call({
      parameters: [{ name: 'note', type: 'string', required: true }],
      values: { note: '' },
      $t: (key) => key,
    });

    expect(errors).to.deep.equal([]);
  });

  it('measures string bounds in UTF-8 bytes like the backend', () => {
    expect(WorkflowRunDialog.methods.stringByteLength('a')).to.equal(1);
    expect(WorkflowRunDialog.methods.stringByteLength('é')).to.equal(2);
    expect(WorkflowRunDialog.methods.stringByteLength('🧪')).to.equal(4);
  });

  it('initializes fields when the dialog is created already open', async () => {
    const wrapper = shallowMount(WorkflowRunDialog, {
      propsData: {
        value: true,
        projectId: 7,
        workflow: {
          parameters: [{ name: 'region', type: 'string' }],
          nodes: [{ id: 11, override_policy: { allow_branch: true } }],
        },
      },
      mocks: { $t: (key) => key },
    });
    await wrapper.vm.$nextTick();

    expect(wrapper.vm.values).to.have.property('region');
    expect(wrapper.vm.nodeValues).to.have.property('11');
  });

  it('renders secret audit data as a reference and fingerprint without a value', () => {
    const snapshot = {
      type: 'secret_reference',
      secret_reference: { access_key_id: 41 },
      reference_fingerprint: 'sha256:abc',
    };

    const rendered = WorkflowParameterAudit.methods.parameterValue.call({
      $t: () => 'Credential',
    }, snapshot);

    expect(rendered).to.equal('Credential #41 · sha256:abc');
    expect(rendered).not.to.contain('value');
  });
});
