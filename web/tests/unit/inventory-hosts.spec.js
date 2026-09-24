import './setup';
import { expect } from 'chai';
import { mount, shallowMount } from '@vue/test-utils';
import Vuetify from 'vuetify';
import axios from 'axios';
import InventoryRefresh from '@/components/enhanced/InventoryRefresh.vue';
import NewTaskDialog from '@/components/NewTaskDialog.vue';
import HostBrowser from '@/components/enhanced/HostBrowser.vue';
import HostTaskHistory from '@/components/enhanced/HostTaskHistory.vue';
import InventoryDetails from '@/views/project/InventoryDetails.vue';
import router from '@/router';

describe('inventory host browser', () => {
  let originalGet;
  let wrapper;
  beforeEach(() => {
    originalGet = axios.get;
  });
  afterEach(() => {
    axios.get = originalGet;
    if (wrapper) wrapper.destroy();
    wrapper = null;
    document.querySelectorAll('[data-app]').forEach((element) => element.remove());
  });

  it('resolves the reference-dialog inventory URL to a detail page', () => {
    const match = router.match('/project/3/inventories/26');
    expect(match.matched).to.have.length(1);
    expect(match.params).to.deep.equal({ projectId: '3', inventoryId: '26' });
  });

  it('opens a mounted refresh form without executing a task on context selection', async () => {
    const app = document.createElement('div');
    app.setAttribute('data-app', 'true');
    const target = document.createElement('div');
    app.appendChild(target);
    document.body.appendChild(app);
    axios.get = async () => ({
      data: {
        id: 11, project_id: 7, app: 'ansible', name: 'Test',
      },
    });
    wrapper = mount(InventoryRefresh, {
      vuetify: new Vuetify(),
      attachTo: target,
      propsData: { projectId: 7, inventoryId: 9 },
      mocks: { $t: (key) => key },
      stubs: { TaskForm: true },
    });
    await wrapper.setData({ templateId: 11 });
    await wrapper.vm.openTask();
    await wrapper.vm.$nextTick();
    const dialog = wrapper.findComponent(NewTaskDialog);
    expect(dialog.vm.dialog).to.equal(true);
    expect(dialog.props('sourceTask')).to.include({ inventory_id: 9 });
    expect(dialog.props('sourceTask').params).to.deep.equal({ inventory_refresh: true });
    expect(dialog.vm.saveButtonText).to.equal('hostsRefresh');
  });

  it('keeps the selected host open when refreshed search results still contain it', async () => {
    const host = {
      inventory_id: 9, inventory_name: 'Production', host: 'web', groups: [],
    };
    axios.get = async () => ({ data: { items: [host] } });
    wrapper = shallowMount(HostBrowser, {
      vuetify: new Vuetify(),
      propsData: { projectId: 7, inventoryId: 9 },
      mocks: { $t: (key) => key },
    });
    await wrapper.vm.load();
    wrapper.vm.selectHost(wrapper.vm.rows[0]);
    await wrapper.vm.load();
    expect(wrapper.vm.expanded.map((item) => item.host)).to.deep.equal(['web']);
  });

  it('identifies user, schedule, integration and workflow triggers independently', () => {
    const context = { $t: (key, data) => `${key}:${data?.id || ''}` };
    const trigger = (task) => HostTaskHistory.methods.trigger.call(context, task);
    expect(trigger({ user_name: 'Dennis' })).to.equal('Dennis');
    expect(trigger({ user_name: 'Dennis', schedule_id: 4 })).to.equal('hostsScheduleTrigger:4');
    expect(trigger({ integration_id: 5 })).to.equal('hostsIntegrationTrigger:5');
    expect(trigger({ workflow_run_id: 6 })).to.equal('hostsWorkflowTrigger:6');
  });

  it('ignores snapshot responses from an earlier project or refresh request', async () => {
    const pending = [];
    axios.get = () => new Promise((resolve) => { pending.push(resolve); });
    const context = {
      projectId: 7, inventoryId: 9, snapshotRequestId: 0, latest: null, revision: 0,
    };
    const load = () => InventoryDetails.methods.loadSnapshots.call(context);
    const previousProject = load();
    context.projectId = 8;
    pending[0]({ data: [{ task_id: 1 }] });
    await previousProject;
    expect(context.latest).to.equal(null);
    const older = load();
    const newer = load();
    pending[2]({ data: [{ task_id: 3 }] });
    await newer;
    pending[1]({ data: [{ task_id: 2 }] });
    await older;
    expect(context.latest.task_id).to.equal(3);
    expect(context.revision).to.equal(1);
  });
});
