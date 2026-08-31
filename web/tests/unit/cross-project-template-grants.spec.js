import { expect } from 'chai';
import axios from 'axios';
import CrossProjectTemplateGrantsDialog from '@/components/CrossProjectTemplateGrantsDialog.vue';
import WorkflowEditor from '@/views/project/WorkflowEditor.vue';

describe('cross-project template UI', () => {
  let originalGet;

  beforeEach(() => {
    originalGet = axios.get;
  });

  afterEach(() => {
    axios.get = originalGet;
  });

  it('builds strict grant commands without persistence-only fields', () => {
    const command = CrossProjectTemplateGrantsDialog.methods.grantCommand({
      consumerProjectId: '12',
      minVersion: 2,
      maxVersion: 4,
      allowReference: true,
      allowRun: true,
      reason: ' approved ',
    });

    expect(command).to.deep.equal({
      consumer_project_id: 12,
      min_version: 2,
      max_version: 4,
      operations: 3,
      reason: 'approved',
    });
  });

  it('limits lifecycle actions to the participating side', () => {
    const methods = CrossProjectTemplateGrantsDialog.methods;
    const pending = {
      owner_project_id: 1, consumer_project_id: 2, status: 'pending',
    };
    const active = { ...pending, status: 'active' };
    const revoked = { ...pending, status: 'revoked' };

    expect(methods.canAcceptGrant.call({ projectId: 2 }, pending)).to.equal(true);
    expect(methods.canAcceptGrant.call({ projectId: 1 }, pending)).to.equal(false);
    expect(methods.canRevokeGrant.call({ projectId: 1 }, active)).to.equal(true);
    expect(methods.canRevokeGrant.call({ projectId: 2 }, active)).to.equal(true);
    expect(methods.canDeleteGrant.call({ projectId: 1 }, pending)).to.equal(true);
    expect(methods.canDeleteGrant.call({ projectId: 1 }, active)).to.equal(false);
    expect(methods.canDeleteGrant.call({ projectId: 1 }, revoked)).to.equal(true);
  });

  it('edits the safe API grant view rather than persistence-only field names', () => {
    const context = {
      editingGrantId: null,
      deletingGrantId: null,
      editForm: null,
    };

    CrossProjectTemplateGrantsDialog.methods.beginEdit.call(context, {
      id: 5,
      consumer_project_id: 2,
      min_version: 3,
      max_version: 4,
      operations: 3,
      reason: 'bounded',
    });

    expect(context.editForm).to.include({ minVersion: 3, maxVersion: 4 });
  });

  it('loads project grants and immutable versions through bounded endpoints', async () => {
    const calls = [];
    axios.get = async (url) => {
      calls.push(url);
      if (url.includes('/versions')) {
        return { data: [{ id: 20, version_number: 3 }] };
      }
      return { data: [{ id: 5, owner_project_id: 7, template_id: 9 }] };
    };
    const context = {
      projectId: 7,
      templateId: 9,
      ownerMode: true,
      loading: false,
      error: null,
      grants: [],
      versions: [],
      revokeReasons: {},
      createForm: CrossProjectTemplateGrantsDialog.methods.emptyGrantForm(),
    };

    await CrossProjectTemplateGrantsDialog.methods.load.call(context);

    expect(calls).to.deep.equal([
      '/api/project/7/cross-project-template-grants?count=100',
      '/api/project/7/templates/9/versions?count=100',
    ]);
    expect(context.createForm.minVersion).to.equal(3);
    expect(context.createForm.maxVersion).to.equal(3);
  });

  it('maps an exact grant version into the existing workflow node model', () => {
    const context = {
      editingNode: {
        template_id: 8,
        task_params: { inventory_id: 9 },
        override_policy: { inventory: true },
      },
      crossProjectReferences: [{
        choice_value: 'grant:5:version:3',
        grant_id: 5,
        template_id: 41,
        template_version_number: 3,
      }],
      applyNodeEditCalled: false,
      applyNodeEdit() { this.applyNodeEditCalled = true; },
    };

    WorkflowEditor.methods.applyTemplateChoice.call(context, 'grant:5:version:3');

    expect(context.editingNode.template_id).to.equal(41);
    expect(context.editingNode.cross_project_template_reference).to.deep.equal({
      grant_id: 5,
      template_version_number: 3,
    });
    expect(context.editingNode.task_params).to.deep.equal({});
    expect(context.editingNode.override_policy).to.deep.equal({});
    expect(context.applyNodeEditCalled).to.equal(true);
  });

  it('switches back to a local template without retaining grant provenance', () => {
    const context = {
      editingNode: {
        template_id: 41,
        cross_project_template_reference: { grant_id: 5, template_version_number: 3 },
      },
      crossProjectReferences: [],
      applyNodeEdit() {},
    };

    WorkflowEditor.methods.applyTemplateChoice.call(context, 'local:8');

    expect(context.editingNode.template_id).to.equal(8);
    expect(context.editingNode).not.to.have.property('cross_project_template_reference');
  });

  it('submits only caller-owned grant coordinates in workflow payloads', () => {
    const context = {
      projectId: 2,
      item: {
        nodes: [{
          id: 1,
          template_id: 41,
          cross_project_template_reference: {
            grant_id: 5,
            template_version_number: 3,
            owner_project_id: 1,
            content_fingerprint: 'server-owned',
          },
        }],
      },
      clone: WorkflowEditor.methods.clone,
    };

    const payload = WorkflowEditor.methods.payload.call(context);

    expect(payload.nodes[0].cross_project_template_reference).to.deep.equal({
      grant_id: 5,
      template_version_number: 3,
    });
  });
});
