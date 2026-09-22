import './setup';
import { expect } from 'chai';
import { shallowMount, createLocalVue } from '@vue/test-utils';
import Vuetify from 'vuetify';
import TaskLogView from '@/components/TaskLogView.vue';
import mockAxios from './helpers/axiosMock';

async function flush() {
  await new Promise((resolve) => { setTimeout(resolve, 0); });
}

function mountView(item) {
  const localVue = createLocalVue();
  return shallowMount(TaskLogView, {
    localVue,
    vuetify: new Vuetify(),
    propsData: { item, projectId: 7 },
    mocks: { $t: (k) => k },
  });
}

describe('TaskLogView.vue websocket handling', () => {
  let http;
  let wrapper;

  beforeEach(async () => {
    http = mockAxios();
    http.respond((config) => (config.url.endsWith('/output') ? [] : {}));
    wrapper = mountView({ id: 42, status: 'running' });
    await flush();
  });

  afterEach(() => {
    wrapper.destroy();
    http.restore();
  });

  [{ tpl_app: 'terraform' }, { tpl_app: 'tofu' },
    { tpl_app: 'terragrunt' }, { tpl_app: 'bash', task_groups: ['group/7'] }]
    .forEach((item) => {
      it(`waits for ${item.tpl_app} exit instead of offering force stop`, async () => {
        await wrapper.setProps({ item: { id: 42, status: 'stopping', ...item } });
        expect(wrapper.vm.awaitingGracefulStop).to.equal(true);
        expect(wrapper.vm.stopLabel).to.equal('taskGracefulStopping');
        expect(wrapper.find('v-btn-stub[color="error"]').attributes('disabled')).to.equal('true');
      });
    });

  it('retains force stop for ungrouped shell tasks', async () => {
    await wrapper.setProps({ item: { id: 42, status: 'stopping', tpl_app: 'bash' } });
    expect(wrapper.vm.awaitingGracefulStop).to.equal(false);
    expect(wrapper.vm.stopLabel).to.equal('forceStop');
  });

  it('ignores messages of other tasks and projects', () => {
    wrapper.vm.onWebsocketDataReceived({
      project_id: 7, task_id: 43, type: 'log', output: 'x', time: 't',
    });
    wrapper.vm.onWebsocketDataReceived({
      project_id: 8, task_id: 42, type: 'log', output: 'x', time: 't',
    });
    expect(wrapper.vm.outputBuffer).to.deep.equal([]);
  });

  it('buffers log records with a derived id', () => {
    wrapper.vm.onWebsocketDataReceived({
      project_id: 7, task_id: 42, type: 'log', output: 'hello', time: '10:00',
    });
    expect(wrapper.vm.outputBuffer).to.have.lengthOf(1);
    expect(wrapper.vm.outputBuffer[0]).to.include({ output: 'hello', id: '10:00hello' });
    expect(wrapper.vm.output).to.deep.equal([]);
  });

  it('merges update messages into the item without copying the message type', () => {
    wrapper.vm.onWebsocketDataReceived({
      project_id: 7, task_id: 42, type: 'update', status: 'success', end: '2026-01-01',
    });
    expect(wrapper.vm.item.status).to.equal('success');
    expect(wrapper.vm.item.end).to.equal('2026-01-01');
    expect(wrapper.vm.item.type).to.equal(undefined);
  });

  it('ignores unknown message types', () => {
    wrapper.vm.onWebsocketDataReceived({ project_id: 7, task_id: 42, type: 'weird' });
    expect(wrapper.vm.outputBuffer).to.deep.equal([]);
    expect(wrapper.vm.item.status).to.equal('running');
  });
});
