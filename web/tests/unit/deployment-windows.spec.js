import './local-storage-fixture';
import { expect } from 'chai';
import DeploymentWindowsPanel from '@/components/DeploymentWindowsPanel.vue';
import TaskForm from '@/components/TaskForm.vue';
import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';
import Settings from '@/views/project/Settings.vue';

describe('deployment window UI contracts', () => {
  it('keeps settings unchanged when the capability is absent', () => {
    expect(Settings.computed.deploymentWindowsDecision.call({ systemInfo: {} })).to.equal(null);
    const decision = {
      id: 'deployment_windows', access: ['read', 'write', 'execute'],
    };
    expect(Settings.computed.deploymentWindowsDecision.call({
      systemInfo: { capabilities: { capabilities: [decision] } },
    })).to.equal(decision);
  });

  it('serializes only the editable policy and selected preview target', () => {
    const policy = {
      project_id: 7,
      revision: 4,
      timezone: 'Europe/Berlin',
      default: 'deny',
      rules: [{
        id: 12,
        revision: 4,
        name: 'Weekdays',
        active: true,
        kind: 'allow',
        scope: 'template',
        template_id: 9,
        workflow_id: 99,
        recurrence: '0 9 * * 1-5',
        duration_minutes: 480,
        effective_from: null,
        effective_until: null,
      }],
    };
    const context = {
      policy,
      previewTarget: 'template:9',
      policyPayload: DeploymentWindowsPanel.methods.policyPayload,
      targetInput: DeploymentWindowsPanel.methods.targetInput,
    };

    expect(DeploymentWindowsPanel.methods.policyPayload.call(context)).to.deep.equal({
      revision: 4,
      timezone: 'Europe/Berlin',
      default: 'deny',
      rules: [{
        id: 12,
        revision: 4,
        name: 'Weekdays',
        active: true,
        kind: 'allow',
        scope: 'template',
        template_id: 9,
        recurrence: '0 9 * * 1-5',
        duration_minutes: 480,
      }],
    });
    expect(DeploymentWindowsPanel.methods.previewPayload.call(context)).to.deep.equal({
      revision: 4,
      timezone: 'Europe/Berlin',
      default: 'deny',
      rules: [{
        id: 12,
        revision: 4,
        name: 'Weekdays',
        active: true,
        kind: 'allow',
        scope: 'template',
        template_id: 9,
        recurrence: '0 9 * * 1-5',
        duration_minutes: 480,
      }],
      template_id: 9,
    });
  });

  it('adds a task override only after a blocked decision and explicit confirmation', () => {
    const base = { project_id: 7, template_id: 11 };
    const context = {
      deploymentWindowBlock: { state: 'blocked', reason: 'freeze_active' },
      deploymentWindowOverrideCategory: 'incident',
      deploymentWindowOverrideReference: 'INC-41',
      deploymentWindowOverrideConfirmed: true,
      deploymentWindowOverrideReady: true,
    };

    expect(TaskForm.methods.taskStartPayload.call(context, base)).to.deep.equal({
      ...base,
      deployment_window_override: { category: 'incident', reference: 'INC-41' },
    });
    context.deploymentWindowOverrideConfirmed = false;
    context.deploymentWindowOverrideReady = false;
    expect(TaskForm.methods.taskStartPayload.call(context, base)).to.equal(base);
  });

  it('releases the task dialog save latch after a blocked start', async () => {
    const emitted = [];
    const blocked = {
      response: { status: 409, data: { state: 'blocked', reason: 'freeze_active' } },
    };
    const payload = { project_id: 7, template_id: 11 };
    const context = {
      formError: null,
      formSaving: false,
      deploymentWindowBlock: null,
      executionPreflight: { fingerprint: 'sha256:plan', review_token: 'review', findings: [] },
      executionPreflightPayloadSignature: JSON.stringify(payload),
      deploymentWindowOverrideReady: false,
      $refs: { form: { validate: () => true } },
      $emit: (...args) => emitted.push(args),
      $t: (key) => key,
      beforeSave: async () => {},
      taskSavePayload: () => payload,
      taskStartPayload: TaskForm.methods.taskStartPayload,
      isDeploymentWindowBlock: TaskForm.methods.isDeploymentWindowBlock,
      adoptDeploymentWindowBlock: TaskForm.methods.adoptDeploymentWindowBlock,
      submitTaskPayload: async () => { throw blocked; },
    };

    await TaskForm.methods.save.call(context);

    expect(context.deploymentWindowBlock).to.deep.equal(blocked.response.data);
    expect(emitted).to.deep.include.members([['error', {}]]);
  });

  it('keeps workflow preflight payload unchanged and adds override only to start', () => {
    const base = { parameters: { region: 'eu' } };
    const context = {
      deploymentWindowBlock: { state: 'blocked', reason: 'default_deny' },
      deploymentWindowOverrideCategory: 'customer_impact',
      deploymentWindowOverrideReference: 'CHG-19',
      deploymentWindowOverrideConfirmed: true,
      deploymentWindowOverrideReady: true,
    };

    expect(WorkflowRunDialog.methods.buildStartPayload.call(context, base)).to.deep.equal({
      ...base,
      deployment_window_override: { category: 'customer_impact', reference: 'CHG-19' },
    });
    expect(base).to.deep.equal({ parameters: { region: 'eu' } });
  });

  it('recognizes only the coarse blocked response', () => {
    expect(WorkflowRunDialog.methods.isDeploymentWindowBlock({
      response: { status: 409, data: { state: 'blocked', reason: 'freeze_active' } },
    })).to.equal(true);
    expect(WorkflowRunDialog.methods.isDeploymentWindowBlock({
      response: { status: 409, data: { preflight: {} } },
    })).to.equal(false);
  });
});
