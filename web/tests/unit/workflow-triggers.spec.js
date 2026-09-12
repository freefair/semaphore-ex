import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import Vuetify from 'vuetify';
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
      vuetify: new Vuetify(),
      propsData: {
        value: false,
        projectId: 7,
        workflow: { id: 9, parameters: [] },
      },
      mocks: {
        $t: (key) => key,
      },
    });

    expect(wrapper.vm.triggerTypes.map(({ value }) => value)).to.deep.equal([
      'manual', 'schedule', 'api', 'webhook',
    ]);
    wrapper.destroy();
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
      credentialAcknowledged: true,
      mutating: false,
      payload: () => ({ name: 'Deploy API', type: 'api' }),
      load: async () => {},
      notifyError: () => {},
      revealCredential: WorkflowTriggersDialog.methods.revealCredential,
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
      credentialAcknowledged: true,
      dismissCredential: WorkflowTriggersDialog.methods.dismissCredential,
      async load() { loaded += 1; },
    };
    await WorkflowTriggersDialog.watch.value.call(reopened, true);
    expect(loaded).to.equal(1);
    expect(reopened.credential).to.equal('');
  });

  it('clears transient credentials when the trigger dialog closes', async () => {
    const closed = {
      credential: 'swhsec_once',
      credentialAcknowledged: true,
      dismissCredential: WorkflowTriggersDialog.methods.dismissCredential,
    };

    await WorkflowTriggersDialog.watch.value.call(closed, false);

    expect(closed.credential).to.equal('');
    expect(closed.credentialAcknowledged).to.equal(false);
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
      credentialAcknowledged: true,
      revealCredential: WorkflowTriggersDialog.methods.revealCredential,
      async mutate(operation) { await operation(); },
    };

    await WorkflowTriggersDialog.methods.rotate.call(context, { id: 13, revision: 2 });

    expect(requests[0]).to.deep.equal({
      url: '/api/project/7/workflows/9/triggers/13/rotate',
      payload: { revision: 2 },
    });
    expect(context.credential).to.equal('swt_rotated');
  });

  it('reveals the server-generated webhook signing secret after creation', async () => {
    axios.post = async () => ({
      data: { trigger: { id: 14 }, webhook_signing_secret: 'swhsec_created' },
    });
    const context = {
      editingId: null,
      baseURL: '/api/project/7/workflows/9/triggers',
      formDialog: true,
      credential: '',
      credentialAcknowledged: true,
      mutating: false,
      payload: () => ({ name: 'Deploy webhook', type: 'webhook' }),
      load: async () => {},
      notifyError: () => {},
      revealCredential: WorkflowTriggersDialog.methods.revealCredential,
    };

    await WorkflowTriggersDialog.methods.save.call(context);

    expect(context.credential).to.equal('swhsec_created');
    expect(context.credentialAcknowledged).to.equal(false);
    expect(context.formDialog).to.equal(false);
  });

  it('uses the signed webhook lifecycle without reusing the API credential route', async () => {
    const requests = [];
    axios.post = async (url, payload) => {
      requests.push({ url, payload });
      return { data: { webhook_signing_secret: 'swhsec_next' } };
    };
    const context = {
      baseURL: '/api/project/7/workflows/9/triggers',
      credential: '',
      credentialAcknowledged: true,
      revealCredential: WorkflowTriggersDialog.methods.revealCredential,
      async mutate(operation) { await operation(); },
      mutateWebhookSigning: null,
    };
    context.mutateWebhookSigning = (...args) => WorkflowTriggersDialog.methods
      .mutateWebhookSigning.call(context, ...args);
    const trigger = { id: 13, revision: 7, type: 'webhook' };

    await WorkflowTriggersDialog.methods.bootstrapWebhookSigning.call(context, trigger);
    expect(requests[0]).to.deep.equal({
      url: '/api/project/7/workflows/9/triggers/13/webhook-signing/bootstrap',
      payload: { revision: 7 },
    });
    expect(context.credential).to.equal('swhsec_next');
    expect(context.credentialAcknowledged).to.equal(false);

    await WorkflowTriggersDialog.methods.stageWebhookSigning.call(context, trigger);
    await WorkflowTriggersDialog.methods.promoteWebhookSigning.call(context, trigger);
    await WorkflowTriggersDialog.methods.revokeWebhookSigning.call(context, trigger);
    expect(requests.map(({ url }) => url)).to.deep.equal([
      '/api/project/7/workflows/9/triggers/13/webhook-signing/bootstrap',
      '/api/project/7/workflows/9/triggers/13/webhook-signing/stage',
      '/api/project/7/workflows/9/triggers/13/webhook-signing/promote',
      '/api/project/7/workflows/9/triggers/13/webhook-signing/revoke',
    ]);
  });

  it('distinguishes staged and retired webhook keys and keeps API rotation separate', () => {
    const methods = WorkflowTriggersDialog.methods;
    const context = {
      hasCurrentWebhookKey: methods.hasCurrentWebhookKey,
      hasNextWebhookKey: methods.hasNextWebhookKey,
    };
    const staged = {
      type: 'webhook',
      current_signing_key_id: 'swhkid_current',
      current_signing_generation: 2,
      next_signing_key_id: 'swhkid_next',
      next_signing_generation: 3,
    };
    const retired = { ...staged, current_signing_generation: 3, next_signing_generation: 2 };

    expect(methods.usesCredential({ type: 'api' })).to.equal(true);
    expect(methods.usesCredential(staged)).to.equal(false);
    expect(methods.canStageWebhookKey.call(context, staged)).to.equal(false);
    expect(methods.hasStagedWebhookKey.call(context, staged)).to.equal(true);
    expect(methods.hasStagedWebhookKey.call(context, retired)).to.equal(false);
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
