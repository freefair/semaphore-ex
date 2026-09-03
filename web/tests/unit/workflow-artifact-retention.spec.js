import '@/../tests/unit/local-storage-fixture';
import { expect } from 'chai';
import axios from 'axios';
import WorkflowArtifactRetentionPanel from '@/components/WorkflowArtifactRetentionPanel.vue';
import Settings from '@/views/project/Settings.vue';
import { USER_PERMISSIONS } from '@/lib/constants';

describe('workflow artifact retention UI', () => {
  let originalGet;
  let originalPut;

  beforeEach(() => {
    originalGet = axios.get;
    originalPut = axios.put;
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.put = originalPut;
  });

  it('uses existing global and project administration scopes', () => {
    expect(WorkflowArtifactRetentionPanel.computed.baseUrl.call({ projectId: null }))
      .to.equal('/api/workflow-artifact-retention');
    expect(WorkflowArtifactRetentionPanel.computed.baseUrl.call({ projectId: 7 }))
      .to.equal('/api/project/7/workflow-artifact-retention');
    expect(Settings.computed.canManageArtifactRetention.call({
      isAdmin: false,
      userPermissions: USER_PERMISSIONS.manageProjectResources,
    })).to.equal(true);
    expect(Settings.computed.canManageArtifactRetention.call({
      isAdmin: false,
      userPermissions: 0,
    })).to.equal(false);
  });

  it('maps effective policy values without allowing the request to select scope or actor', () => {
    const context = {
      projectId: 7,
      state: null,
      form: {},
      expectedRevision: 3,
    };
    WorkflowArtifactRetentionPanel.methods.applyState.call(context, {
      global_policy: {
        revision: 2,
        retention_seconds: 604800,
        max_artifact_bytes: 16777216,
        max_run_bytes: 67108864,
      },
      project_policy: {
        revision: 3,
        retention_seconds: 86400,
        max_artifact_bytes: 8388608,
        max_run_bytes: 33554432,
      },
      effective: {},
    });
    expect(context.form).to.deep.equal({
      retentionHours: 24,
      maxArtifactMiB: 8,
      maxRunMiB: 32,
    });
    expect(WorkflowArtifactRetentionPanel.methods.updatePayload.call(context)).to.deep.equal({
      expected_revision: 3,
      retention_seconds: 86400,
      max_artifact_bytes: 8388608,
      max_run_bytes: 33554432,
    });
  });

  it('reloads current policy after an optimistic-save conflict', async () => {
    const state = {
      global_policy: null,
      effective: {
        global_revision: 0,
        retention_seconds: 2592000,
        max_artifact_bytes: 67108864,
        max_run_bytes: 268435456,
      },
    };
    axios.put = async () => {
      throw Object.assign(new Error('conflict'), { response: { status: 409 } });
    };
    axios.get = async () => ({ data: state });
    const context = {
      projectId: null,
      baseUrl: '/api/workflow-artifact-retention',
      state,
      form: { retentionHours: 720, maxArtifactMiB: 64, maxRunMiB: 256 },
      expectedRevision: 0,
      canSave: true,
      saving: false,
      loading: false,
      error: '',
      $t: (key) => key,
      updatePayload: WorkflowArtifactRetentionPanel.methods.updatePayload,
      applyState: WorkflowArtifactRetentionPanel.methods.applyState,
      load: WorkflowArtifactRetentionPanel.methods.load,
    };
    await WorkflowArtifactRetentionPanel.methods.save.call(context);
    expect(context.error).to.equal('workflowArtifactRetentionConflict');
    expect(context.saving).to.equal(false);
  });
});
