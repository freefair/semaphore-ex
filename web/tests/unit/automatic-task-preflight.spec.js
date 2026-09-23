import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import TaskForm from '@/components/TaskForm.vue';

const settle = () => new Promise((resolve) => { setTimeout(resolve, 350); });
const plan = (id) => ({ fingerprint: id, review_token: `token-${id}`, findings: [] });

describe('automatic task execution review', () => {
  let originalPost;
  let wrapper;

  beforeEach(() => { originalPost = axios.post; });
  afterEach(() => {
    if (wrapper) wrapper.destroy();
    wrapper = null;
    axios.post = originalPost;
  });

  function open() {
    wrapper = shallowMount(TaskForm, {
      propsData: {
        itemId: 'new',
        projectId: 7,
        template: {
          id: 11, project_id: 7, app: 'bash', type: '', survey_vars: [],
        },
      },
      mocks: { $t: (key) => key },
    });
  }

  it('loads a review on opening and starts on the first Run click', async () => {
    const calls = [];
    axios.post = async (url, body) => {
      calls.push({ url, body });
      return { data: plan('first') };
    };
    open();
    await settle();
    expect(calls).to.have.length(1);
    expect(wrapper.vm.executionReady).to.equal(true);
    expect(wrapper.vm.executionPreflight.fingerprint).to.equal('first');
    wrapper.vm.$refs.form.validate = () => true;
    let submitted;
    wrapper.vm.submitTaskPayload = async (body, headers) => { submitted = { body, headers }; };
    await wrapper.vm.save();
    expect(submitted.body).to.deep.equal(calls[0].body);
    expect(submitted.headers['X-Semaphore-Preflight-Fingerprint']).to.equal('first');
    expect(calls).to.have.length(1);
  });

  it('disables Run during edits and ignores an older response arriving last', async () => {
    const pending = [];
    axios.post = (url, body) => new Promise((resolve) => { pending.push({ body, resolve }); });
    open();
    await settle();
    expect(pending).to.have.length(1);
    await wrapper.setData({ editedEnvironment: { region: 'new' } });
    expect(wrapper.vm.executionReady).to.equal(false);
    await settle();
    expect(pending).to.have.length(2);
    pending[1].resolve({ data: plan('new') });
    await settle();
    pending[0].resolve({ data: plan('old') });
    await settle();
    expect(wrapper.vm.executionPreflight.fingerprint).to.equal('new');
    expect(wrapper.vm.executionReady).to.equal(true);
    expect(JSON.parse(wrapper.vm.executionPreflightPayloadSignature).environment)
      .to.equal('{"region":"new"}');
  });

  it('keeps Run disabled on a failed preview and permits an explicit retry', async () => {
    axios.post = async () => { throw new Error('Preview unavailable'); };
    open();
    await settle();
    expect(wrapper.vm.executionReady).to.equal(false);
    expect(wrapper.vm.executionPreflightError).to.contain('Preview unavailable');
    axios.post = async () => ({ data: plan('retry') });
    await wrapper.vm.refreshExecutionPreflight();
    expect(wrapper.vm.executionReady).to.equal(true);
  });

  it('keeps denied plans visible without enabling Run', async () => {
    axios.post = async () => ({ data: { ...plan('denied'), findings: [{ severity: 'denial' }] } });
    open();
    await settle();
    expect(wrapper.vm.executionPreflight.fingerprint).to.equal('denied');
    expect(wrapper.vm.executionReady).to.equal(false);
  });

  it('discards pending responses after the dialog closes', async () => {
    let respond;
    axios.post = () => new Promise((resolve) => { respond = resolve; });
    open();
    await settle();
    const form = wrapper.vm;
    wrapper.destroy();
    wrapper = null;
    respond({ data: plan('closed') });
    await settle();
    expect(form.executionPreflight).to.equal(null);
  });

  it('preserves the existing capability-unavailable compatibility fallback', async () => {
    axios.post = async () => {
      throw Object.assign(new Error('Unavailable'), {
        response: {
          status: 404, data: { error: 'CAPABILITY_DENIED', capability: 'execution_preflight' },
        },
      });
    };
    open();
    await settle();
    expect(wrapper.vm.executionReady).to.equal(true);
    wrapper.vm.$refs.form.validate = () => true;
    let submitted = false;
    wrapper.vm.submitTaskPayload = async (body, headers) => {
      submitted = true;
      expect(headers).to.equal(undefined);
    };
    await wrapper.vm.save();
    expect(submitted).to.equal(true);
  });

  it('requires another Run click for genuine drift and uses that fresh review', async () => {
    axios.post = async () => ({ data: plan('first') });
    open();
    await settle();
    wrapper.vm.$refs.form.validate = () => true;
    let submits = 0;
    wrapper.vm.submitTaskPayload = async () => {
      submits += 1;
      throw Object.assign(new Error('Plan changed'), {
        response: { status: 409, data: { preflight: plan('changed') } },
      });
    };
    await wrapper.vm.save();
    expect(submits).to.equal(1);
    expect(wrapper.vm.formError).to.equal('executionPreflightChanged');
    expect(wrapper.vm.executionPreflight.fingerprint).to.equal('changed');
    let headers;
    wrapper.vm.submitTaskPayload = async (body, reviewHeaders) => { headers = reviewHeaders; };
    await wrapper.vm.save();
    expect(headers['X-Semaphore-Preflight-Fingerprint']).to.equal('changed');
  });
});
