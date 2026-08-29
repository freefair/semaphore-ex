import { expect } from 'chai';
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
