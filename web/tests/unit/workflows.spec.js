import './setup';
import { expect } from 'chai';
import { mount } from '@vue/test-utils';

import axios from 'axios';
import Workflows from '@/views/project/Workflows.vue';

describe('workflow approvals inbox', () => {
  let originalGet;

  beforeEach(() => {
    originalGet = axios.get;
  });

  afterEach(() => {
    axios.get = originalGet;
  });

  it('loads only the server-authorized pending approval inbox into the existing workflow page', async () => {
    let request;
    axios.get = async (url) => {
      request = url;
      return { data: [{ workflow_template_id: 41, workflow_run_id: 91, status: 'pending' }] };
    };
    const context = {
      projectId: 7,
      approvalInboxDialog: false,
      approvalInboxLoading: false,
      approvalInbox: [],
      $t: (key) => key,
    };

    await Workflows.methods.openApprovalInbox.call(context);

    expect(request).to.equal('/api/project/7/workflow-approvals');
    expect(context.approvalInboxDialog).to.equal(true);
    expect(context.approvalInbox).to.deep.equal([{ workflow_template_id: 41, workflow_run_id: 91, status: 'pending' }]);
    expect(context.approvalInboxLoading).to.equal(false);
  });
});

describe('workflow action column permission', () => {
  it('keeps the action column for a start-only workflow role', () => {
    const context = {
      can: (permission) => permission === 128,
    };

    expect(Workflows.methods.allowActions.call(context)).to.equal(true);
  });
});

describe('workflow list scoped quick run', () => {
  it('shows quick run only for rows with effective start access and no global grant', async () => {
    const wrapper = mount(Workflows, {

      props: { projectId: 7 },
      methods: { can: () => false, loadItems: async () => {} },
      global: {
        renderStubDefaultSlot: true,
        mocks: { $t: (key) => key, $i18n: { t: (key) => key }, $router: { push: () => {} } },
        stubs: {
          'router-link': { template: '<span><slot /></span>' },
          'v-data-table': {
            props: ['headers', 'items'],
            template: `<div><div v-for="item in items" :key="item.id" :data-row="item.id">
              <slot v-if="headers.some((h) => h.key === 'actions')"
                name="item.actions" :item="item" />
            </div></div>`,
          },
          TableSettingsSheet: true,
          WorkflowRunDialog: true,
          YesNoDialog: true,
        },
      },
    });
    await wrapper.setData({
      filteredHeaders: wrapper.vm.headers,
      items: [
        { id: 1, name: 'Granted', effective_access: { start: true } },
        { id: 2, name: 'Denied', effective_access: { start: false } },
      ],
    });
    expect(wrapper.find('[data-row="1"] button[title="workflowRunNow"]').exists()).to.equal(true);
    expect(wrapper.find('[data-row="2"] button[title="workflowRunNow"]').exists()).to.equal(false);
    await wrapper.find('[data-row="1"] button[title="workflowRunNow"]').trigger('click');
    expect(wrapper.vm.selectedWorkflow.id).to.equal(1);
    expect(wrapper.vm.runDialog).to.equal(true);
    wrapper.unmount();
  });
});
