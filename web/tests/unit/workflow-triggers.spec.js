import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import WorkflowTriggersDialog from '@/components/WorkflowTriggersDialog.vue';
import WorkflowView from '@/views/project/WorkflowView.vue';

describe('workflow triggers', () => {
  let originalGet;
  let originalPost;
  let originalPut;

  beforeEach(() => {
    originalGet = axios.get;
    originalPost = axios.post;
    originalPut = axios.put;
    axios.get = async () => ({ data: [] });
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.post = originalPost;
    axios.put = originalPut;
  });

  it('offers exactly the four trigger resource types in one focused component', () => {
    const wrapper = shallowMount(WorkflowTriggersDialog, {
      propsData: {
        value: false,
        projectId: 7,
        workflow: { id: 9, parameters: [] },
      },
      mocks: {
        $t: (key) => key,
        $vuetify: { breakpoint: { xsOnly: false } },
      },
    });

    expect(wrapper.vm.triggerTypes.map(({ value }) => value)).to.deep.equal([
      'manual', 'schedule', 'api', 'webhook',
    ]);
  });

  it('serializes only explicit fixed and request mappings', () => {
    const context = {
      parameters: [
        { name: 'region', type: 'string' },
        { name: 'confirm', type: 'boolean' },
        { name: 'ignored', type: 'string' },
      ],
      form: {
        name: 'Deploy API',
        type: 'api',
        enabled: true,
        cron_format: '',
        revision: 2,
        sources: { region: 'request', confirm: 'fixed', ignored: 'none' },
        requestKeys: { region: 'target' },
        fixedValues: { confirm: false },
      },
      fixedValue: WorkflowTriggersDialog.methods.fixedValue,
    };

    const payload = WorkflowTriggersDialog.methods.payload.call(context);

    expect(payload.input_mappings).to.deep.equal([
      { parameter: 'region', source: 'request', key: 'target' },
      { parameter: 'confirm', source: 'fixed', value: false },
    ]);
    expect(JSON.stringify(payload)).not.to.contain('ignored');
  });

  it('keeps a created credential in transient component state only', async () => {
    const requests = [];
    axios.post = async (url, payload) => {
      requests.push({ url, payload });
      return { data: { trigger: { id: 13 }, credential: 'swt_once' } };
    };
    const context = {
      editingId: null,
      baseURL: '/api/project/7/workflows/9/triggers',
      formDialog: true,
      credential: '',
      mutating: false,
      payload: () => ({ name: 'Deploy API', type: 'api' }),
      load: async () => {},
      notifyError: () => {},
    };

    await WorkflowTriggersDialog.methods.save.call(context);

    expect(requests).to.deep.equal([{
      url: '/api/project/7/workflows/9/triggers',
      payload: { name: 'Deploy API', type: 'api' },
    }]);
    expect(context.credential).to.equal('swt_once');
    expect(context.formDialog).to.equal(false);

    let loaded = 0;
    const reopened = {
      credential: context.credential,
      async load() { loaded += 1; },
    };
    await WorkflowTriggersDialog.watch.value.call(reopened, true);
    expect(loaded).to.equal(1);
    expect(reopened.credential).to.equal('');
  });

  it('rotates through the lifecycle endpoint and replaces the transient credential', async () => {
    const requests = [];
    axios.post = async (url, payload) => {
      requests.push({ url, payload });
      return { data: { credential: 'swt_rotated' } };
    };
    const context = {
      baseURL: '/api/project/7/workflows/9/triggers',
      credential: 'swt_old',
      async mutate(operation) { await operation(); },
    };

    await WorkflowTriggersDialog.methods.rotate.call(context, { id: 13, revision: 2 });

    expect(requests[0]).to.deep.equal({
      url: '/api/project/7/workflows/9/triggers/13/rotate',
      payload: { revision: 2 },
    });
    expect(context.credential).to.equal('swt_rotated');
  });

  it('initializes typed values for test-fire request mappings', () => {
    const trigger = { id: 13 };
    const context = {
      testingTrigger: null,
      testDialog: false,
      testValues: {},
      testRequestMappings: [
        { key: 'confirm', parameterDefinition: { type: 'boolean' } },
        { key: 'retries', parameterDefinition: { type: 'integer' } },
        {
          key: 'credential',
          parameterDefinition: {
            type: 'secret_reference',
            secret_options: [{ access_key_id: 21, label: 'Deploy key' }],
          },
        },
      ],
      initialFixedValue: WorkflowTriggersDialog.methods.initialFixedValue,
      $set(target, key, value) { Reflect.set(target, key, value); },
    };

    WorkflowTriggersDialog.methods.beginTest.call(context, trigger);

    expect(context.testingTrigger).to.equal(trigger);
    expect(context.testValues).to.deep.equal({
      confirm: false,
      retries: 0,
      credential: { access_key_id: 21 },
    });
    expect(context.testDialog).to.equal(true);
  });

  it('keeps the host hook hidden without the Enhanced trigger capability', () => {
    expect(WorkflowView.computed.triggersAvailable.call({ triggerDecision: null })).to.equal(false);
    expect(WorkflowView.computed.triggersAvailable.call({
      triggerDecision: { access: ['read', 'write', 'execute'] },
    })).to.equal(true);
  });
});
