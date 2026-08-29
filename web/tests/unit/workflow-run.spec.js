import { expect } from 'chai';
import axios from 'axios';
import WorkflowRun from '@/views/project/WorkflowRun.vue';
import WorkflowRuns from '@/views/project/workflow/WorkflowRuns.vue';

describe('linear workflow run dashboard', () => {
  let originalGet;

  beforeEach(() => {
    originalGet = axios.get;
  });

  afterEach(() => {
    axios.get = originalGet;
  });

  it('maps task states without hiding distinct blocked, skipped, or canceled nodes', () => {
    const context = {
      details: {
        nodes: [
          { node: { id: 11 }, status: 'succeeded', task: { id: 101, status: 'success' } },
          { node: { id: 12 }, status: 'blocked' },
          { node: { id: 13 }, status: 'skipped' },
          { node: { id: 14 }, status: 'canceled' },
        ],
      },
      normalizeNodeStatus: WorkflowRun.methods.normalizeNodeStatus,
    };

    expect(WorkflowRun.computed.nodeStatuses.call(context)).to.deep.equal({
      11: 'success',
      12: 'blocked',
      13: 'skipped',
      14: 'canceled',
    });
    expect(WorkflowRuns.methods.statusColor('succeeded')).to.equal('success');
    expect(WorkflowRuns.methods.statusColor('blocked')).to.equal('error');
    expect(WorkflowRuns.methods.statusColor('queued')).to.equal('primary');
  });

  it('reports compact active-node progress against the frozen workflow bound', () => {
    const context = {
      workflow: { max_parallel_tasks: 3 },
      details: {
        nodes: [
          { status: 'queued' },
          { status: 'running' },
          { status: 'skipped' },
          { status: 'succeeded' },
        ],
      },
    };

    expect(WorkflowRun.computed.parallelProgress.call(context)).to.deep.equal({
      active: 2,
      max: 3,
    });
  });

  it('loads the immutable dashboard contract without fetching the edited live workflow', async () => {
    const requests = [];
    axios.get = async (url) => {
      requests.push(url);
      if (url.endsWith('/artifacts')) {
        return {
          data: [{
            workflow_node_id: 11,
            name: 'deployment_token',
            schema: { type: 'string' },
            sensitive: true,
            availability: 'available',
          }],
        };
      }
      return {
        data: {
          run: { id: 91, status: 'queued' },
          workflow: {
            id: 41, name: 'Frozen', nodes: [], edges: [],
          },
          templates: [{ id: 51, name: 'Frozen template' }],
          nodes: [],
        },
      };
    };
    const context = {
      projectId: 7,
      workflowId: 41,
      runId: 91,
      details: null,
      workflow: null,
      templates: [],
      artifacts: [],
      $t: (key) => key,
    };

    await WorkflowRun.methods.loadData.call(context);

    expect(requests).to.deep.equal([
      '/api/project/7/workflows/41/runs/91',
      '/api/project/7/workflows/41/runs/91/artifacts',
    ]);
    expect(context.workflow.name).to.equal('Frozen');
    expect(context.templates[0].name).to.equal('Frozen template');
    expect(context.artifacts[0]).to.include({
      name: 'deployment_token', sensitive: true, availability: 'available',
    });
  });

  it('flattens value-free resolved input provenance for the compact metadata panel', () => {
    const context = {
      details: {
        nodes: [{
          node: { id: 12 },
          artifact_inputs: [{
            name: 'token',
            source_node_id: 11,
            output: 'deployment_token',
            required: true,
            sensitive: true,
            availability: 'available',
            producer_task_id: 301,
            producer_attempt: 2,
          }],
        }],
      },
    };

    expect(WorkflowRun.computed.resolvedArtifactInputs.call(context)).to.deep.equal([{
      name: 'token',
      source_node_id: 11,
      output: 'deployment_token',
      required: true,
      sensitive: true,
      availability: 'available',
      producer_task_id: 301,
      producer_attempt: 2,
      consumer_node_id: 12,
    }]);
  });

  it('counts effective parameters and node overrides for the compact audit panel', () => {
    const details = {
      run: {
        parameters: {
          region: { type: 'string', value: 'eu', source: 'user' },
          token: {
            type: 'secret_reference',
            secret_reference: { access_key_id: 41 },
            reference_fingerprint: 'sha256:abc',
          },
        },
      },
      nodes: [
        { node: { id: 11 }, overrides: { inventory_id: 5 } },
        { node: { id: 12 }, overrides: {} },
      ],
    };
    const parameterEntries = Object.entries(details.run.parameters)
      .map(([name, snapshot]) => ({ name, snapshot }));
    const overrideEntries = details.nodes
      .filter((entry) => Object.keys(entry.overrides).length)
      .map((entry) => ({ nodeId: entry.node.id, overrides: entry.overrides }));

    expect(parameterEntries).to.have.length(2);
    expect(overrideEntries).to.have.length(1);
  });

  it('refreshes only when an existing task in this run changes', () => {
    let reloads = 0;
    const context = {
      projectId: 7,
      details: { nodes: [{ task: { id: 301 } }, { status: 'pending' }] },
      loadData() { reloads += 1; },
    };

    WorkflowRun.methods.onWebsocketDataReceived.call(context, {
      type: 'update', project_id: 7, task_id: 301,
    });
    WorkflowRun.methods.onWebsocketDataReceived.call(context, {
      type: 'update', project_id: 8, task_id: 301,
    });
    WorkflowRun.methods.onWebsocketDataReceived.call(context, {
      type: 'update', project_id: 7, task_id: 999,
    });

    expect(reloads).to.equal(1);
  });

  it('shows deterministic elapsed time and keeps queued runs stoppable', () => {
    const start = '2026-08-28T12:00:00Z';
    const end = '2026-08-28T12:01:05Z';
    const elapsed = WorkflowRun.methods.formatElapsed(start, end);
    expect(elapsed).to.equal('1m 5s');
    expect(WorkflowRun.methods.isActiveRunStatus('queued')).to.equal(true);
    expect(WorkflowRun.methods.isActiveRunStatus('stopping')).to.equal(true);
    expect(WorkflowRun.methods.isActiveRunStatus('succeeded')).to.equal(false);
  });

  it('shows and retries a quarantined reconciliation through the existing run toolbar', async () => {
    const context = {
      details: {
        run: {
          reconciliation_state: 'quarantined',
          reconciliation_attempts: 3,
          reconciliation_last_error: 'temporary database failure',
        },
      },
      can: () => true,
      USER_PERMISSIONS: { runProjectTasks: 1 },
    };
    expect(WorkflowRun.computed.reconciliationQuarantined.call(context)).to.equal(true);

    const requests = [];
    const originalPost = axios.post;
    axios.post = async (url) => { requests.push(url); return { data: {} }; };
    const retryContext = {
      projectId: 7,
      workflowId: 41,
      runId: 91,
      retryingReconciliation: false,
      loadData: async () => {},
      $t: (key) => key,
    };
    await WorkflowRun.methods.retryReconciliation.call(retryContext);
    axios.post = originalPost;

    expect(requests).to.deep.equal(['/api/project/7/workflows/41/runs/91/retry-reconcile']);
    expect(retryContext.retryingReconciliation).to.equal(false);
  });

  it('summarizes an HA ownership transfer without exposing workflow values', () => {
    const ownership = {
      owned: true,
      owner_boot_id: 'boot-b-12345678',
      previous_owner_boot_id: 'boot-a-87654321',
      transfer_count: 2,
      reconciliation_lag_seconds: 1,
      recovered: true,
    };
    expect(WorkflowRun.computed.reconciliationOwnership.call({
      details: { run: { reconciliation_ownership: ownership } },
    })).to.equal(ownership);
    const context = { $t: (key, values) => ({ key, values }) };
    expect(WorkflowRun.methods.workflowOwnershipSummary.call(context, ownership)).to.deep.equal({
      key: 'workflowReconciliationOwnershipTransferred',
      values: { owner: 'boot-b-1', transfers: 2, lag: 1 },
    });
  });

  it('labels durable stopping and recovery states without changing task-node status mapping', () => {
    const context = { $t: (key) => key };
    expect(WorkflowRun.methods.runStatusLabel.call(context, 'stopping')).to.equal('workflowRunStopping');
    expect(WorkflowRun.methods.runStatusLabel.call(context, 'canceled')).to.equal('workflowRunStopped');
    expect(WorkflowRun.methods.runStatusLabel.call(context, 'running')).to.equal('running');
    expect(WorkflowRun.methods.statusColor.call({ normalizeNodeStatus: (value) => value }, 'stopping')).to.equal('warning');
  });

  it('renders approval state from immutable request snapshots and sends bounded user decisions', async () => {
    const context = {
      details: {
        nodes: [{ node: { id: 21 }, status: 'approval' }],
        approvals: [
          {
            workflow_node_id: 21,
            status: 'pending',
            prompt: 'Deploy?',
            deadline: '2026-08-29T12:00:00Z',
          },
          { workflow_node_id: 22, status: 'expired', prompt: 'Older deploy' },
        ],
      },
      normalizeNodeStatus: WorkflowRun.methods.normalizeNodeStatus,
    };

    expect(WorkflowRun.computed.nodeStatuses.call(context)).to.deep.equal({ 21: 'approval' });
    const pendingApproval = WorkflowRun.computed.pendingApprovals.call(context)
      .find((approval) => approval.nodeId === 21);
    expect(pendingApproval)
      .to.include({ status: 'pending', prompt: 'Deploy?' });
    const resolvedApproval = WorkflowRun.computed.resolvedApprovals.call(context)
      .find((approval) => approval.nodeId === 22);
    expect(resolvedApproval)
      .to.include({ status: 'expired', prompt: 'Older deploy' });

    const requests = [];
    const originalPost = axios.post;
    axios.post = async (url, payload) => { requests.push({ url, payload }); return { data: {} }; };
    const decisionContext = {
      projectId: 7,
      workflowId: 41,
      runId: 91,
      approvalComments: { 21: 'Reviewed' },
      loadData: async () => {},
      $t: (key) => key,
    };
    await WorkflowRun.methods.resolveApproval.call(decisionContext, 21, 'approved');
    axios.post = originalPost;

    expect(requests).to.deep.equal([{
      url: '/api/project/7/workflows/41/runs/91/approvals/21',
      payload: { status: 'approved', comment: 'Reviewed', source: 'user' },
    }]);
  });
});
