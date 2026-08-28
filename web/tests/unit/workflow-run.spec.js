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

  it('maps legacy task states without hiding a blocked workflow node', () => {
    const context = {
      details: {
        nodes: [
          { node: { id: 11 }, status: 'succeeded', task: { id: 101, status: 'success' } },
          { node: { id: 12 }, status: 'blocked' },
        ],
      },
      normalizeNodeStatus: WorkflowRun.methods.normalizeNodeStatus,
    };

    expect(WorkflowRun.computed.nodeStatuses.call(context)).to.deep.equal({
      11: 'success',
      12: 'blocked',
    });
    expect(WorkflowRuns.methods.statusColor('succeeded')).to.equal('success');
    expect(WorkflowRuns.methods.statusColor('blocked')).to.equal('error');
    expect(WorkflowRuns.methods.statusColor('queued')).to.equal('primary');
  });

  it('loads the immutable dashboard contract without fetching the edited live workflow', async () => {
    const requests = [];
    axios.get = async (url) => {
      requests.push(url);
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
      $t: (key) => key,
    };

    await WorkflowRun.methods.loadData.call(context);

    expect(requests).to.deep.equal(['/api/project/7/workflows/41/runs/91']);
    expect(context.workflow.name).to.equal('Frozen');
    expect(context.templates[0].name).to.equal('Frozen template');
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
    expect(WorkflowRun.methods.isActiveRunStatus('succeeded')).to.equal(false);
  });
});
