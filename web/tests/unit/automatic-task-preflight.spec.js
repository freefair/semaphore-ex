import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import TaskForm from '@/components/TaskForm.vue';
import ExecutionPreflightReview from '@/components/ExecutionPreflightReview.vue';

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
    expect(submitted.headers['X-Semaphore-Preflight-Token']).to.equal('token-first');
    expect(calls).to.have.length(1);
  });

  it('keeps the old review visible during refresh and identifies a changed plan', async () => {
    axios.post = async () => ({ data: plan('first') });
    open();
    await settle();
    let respond;
    axios.post = () => new Promise((resolve) => { respond = resolve; });
    const refreshing = wrapper.vm.refreshExecutionPreflight();
    await wrapper.vm.$nextTick();
    expect(wrapper.vm.executionReady).to.equal(false);
    expect(wrapper.findComponent(ExecutionPreflightReview).props('plan').fingerprint).to.equal('first');
    respond({ data: plan('changed') });
    await refreshing;
    expect(wrapper.vm.executionPreflight.fingerprint).to.equal('changed');
    expect(wrapper.vm.formError).to.equal('executionPreflightChanged');
  });

  it('schedules expiry using server time and avoids loops with invalid timing metadata', () => {
    const originalTimeout = global.setTimeout;
    const originalClear = global.clearTimeout;
    const scheduled = [];
    const cleared = [];
    const context = { executionPreflightTimer: 'previous', refreshExecutionPreflight() {} };
    global.setTimeout = (callback, delay) => { scheduled.push(delay); return 'next'; };
    global.clearTimeout = (timer) => { cleared.push(timer); };
    try {
      const review = { expires_at: '2020-01-01T00:05:00Z' };
      TaskForm.methods.scheduleExecutionPreflightExpiry.call(context, review, '2020-01-01T00:00:00Z');
      expect(scheduled).to.deep.equal([295000]);
      expect(cleared).to.deep.equal(['previous']);
      TaskForm.methods.scheduleExecutionPreflightExpiry.call(context, review, undefined);
      TaskForm.methods.scheduleExecutionPreflightExpiry.call(context, review, '2020-01-01T00:05:00Z');
      expect(scheduled).to.have.length(1);
      expect(cleared).to.deep.equal(['previous', 'next', 'next']);
    } finally {
      global.setTimeout = originalTimeout;
      global.clearTimeout = originalClear;
    }
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
    let expiry;
    wrapper.vm.scheduleExecutionPreflightExpiry = (...args) => { expiry = args; };
    wrapper.vm.submitTaskPayload = async () => {
      submits += 1;
      throw Object.assign(new Error('Plan changed'), {
        response: {
          status: 409, data: { preflight: plan('changed') }, headers: { date: 'server time' },
        },
      });
    };
    await wrapper.vm.save();
    expect(submits).to.equal(1);
    expect(wrapper.vm.formError).to.equal('executionPreflightChanged');
    expect(wrapper.vm.executionPreflight.fingerprint).to.equal('changed');
    expect(expiry).to.deep.equal([plan('changed'), 'server time']);
    let headers;
    wrapper.vm.submitTaskPayload = async (body, reviewHeaders) => { headers = reviewHeaders; };
    await wrapper.vm.save();
    expect(headers['X-Semaphore-Preflight-Fingerprint']).to.equal('changed');
  });
});
