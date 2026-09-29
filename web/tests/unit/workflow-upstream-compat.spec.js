import { expect } from 'chai';
import WorkflowEditor from '@/views/project/WorkflowEditor.vue';
import WorkflowGraph from '@/components/WorkflowGraph.vue';
import WorkflowRun from '@/views/project/WorkflowRun.vue';
import WorkflowNodeCard from '@/components/workflow/WorkflowNodeCard.vue';
import WorkflowHistory from '@/lib/workflowHistory';
import { edgeRunState, edgeConditionMet, statusKind } from '@/lib/workflowGraph';

describe('workflow upstream compatibility', () => {
  it('resets undo history when saved IDs or restored definitions replace the baseline', () => {
    const history = new WorkflowHistory(50);
    history.reset({ nodes: [{ id: -1 }], edges: [] });
    history.push({ nodes: [{ id: -1 }, { id: -2 }], edges: [] });
    const context = {
      history,
      item: { nodes: [{ id: 101 }], edges: [] },
      syncHistoryFlags: WorkflowEditor.methods.syncHistoryFlags,
    };
    WorkflowEditor.watch.baseline.call(context);
    expect(history.canUndo()).to.equal(false);
    expect(context.canUndo).to.equal(false);
  });

  it('keeps expressions and stable edge IDs when the new canvas exports a graph', () => {
    const data = {
      1: {
        data: { nodeId: 10, node: { kind: 'task' } },
        pos_x: 0,
        pos_y: 0,
        outputs: { output_1: { connections: [{ node: '2' }, { node: '3' }] } },
      },
      2: { data: { nodeId: 11, node: {} }, outputs: {} },
      3: { data: { nodeId: 12, node: {} }, outputs: {} },
    };
    const context = {
      editor: { export: () => ({ drawflow: { Home: { data } } }) },
      conditions: { '10->11': 'expression', '10->12': 'always' },
      edgeMetadata: { '10->11': { id: -2, condition_expression: 'result.successful', label: 'Gate' } },
      condKey: WorkflowGraph.methods.condKey,
      nextEdgeId: WorkflowGraph.methods.nextEdgeId,
    };
    const first = WorkflowGraph.methods.exportModel.call(context);
    const second = WorkflowGraph.methods.exportModel.call(context);
    expect(first.edges[0]).to.include({
      id: -2, condition: 'expression', condition_expression: 'result.successful', label: 'Gate',
    });
    expect(first.edges[1].id).to.equal(-3);
    expect(second.edges).to.deep.equal(first.edges);
  });

  it('updates the canvas condition together with expression metadata', () => {
    const context = {
      conditions: {}, edgeMetadata: {}, emitChange() {}, condKey: WorkflowGraph.methods.condKey,
    };
    WorkflowGraph.methods.syncEdge.call(context, {
      id: 4,
      source_node_id: 1,
      destination_node_id: 2,
      condition: 'expression',
      condition_expression: 'result.successful',
    });
    expect(context.conditions['1->2']).to.equal('expression');
    expect(context.edgeMetadata['1->2'].condition_expression).to.equal('result.successful');
  });

  it('uses per-approval eligibility and retains the immutable display name in cards', () => {
    const context = {
      details: {
        nodes: [{ node: { id: 5, kind: 'approval', display_name: 'Release gate' }, status: 'approval' }],
        approvals: [{ workflow_node_id: 5, status: 'pending', eligible: false }],
      },
      normalizeNodeStatus: WorkflowRun.methods.normalizeNodeStatus,
    };
    const run = WorkflowRun.computed.nodeRuns.call(context)[5];
    expect(run.status).to.equal('pending');
    expect(run.eligible).to.equal(false);
    expect(WorkflowNodeCard.computed.showApprovalActions.call({
      kind: 'approval',
      status: 'pending',
      run,
      store: { canResolveApprovals: true },
    })).to.equal(false);
    expect(WorkflowNodeCard.computed.title.call({ node: { display_name: 'Release gate' } })).to.equal('Release gate');
  });
});

describe('workflow EX run presentation', () => {
  it('keeps skipped and blocked nodes distinct and does not infer expressions in the browser', () => {
    expect(statusKind('blocked')).to.equal('blocked');
    expect(statusKind('skipped')).to.equal('skipped');
    expect(statusKind('canceled')).to.equal('canceled');
    expect(edgeConditionMet('always', 'skipped')).to.equal(false);
    expect(edgeConditionMet('on_failure', 'canceled')).to.equal(true);
    expect(edgeRunState(
      { source_node_id: 1, destination_node_id: 2, condition: 'expression' },
      { 1: { status: 'success' }, 2: { status: 'running' } },
    )).to.equal(null);
  });
});

describe('workflow editor review regressions', () => {
  it('groups expression keystrokes into one edge undo step', () => {
    const context = {
      editingEdge: { source_node_id: 1, destination_node_id: 2, condition: 'expression' },
      edgeKey: WorkflowEditor.methods.edgeKey,
      $refs: { graph: { syncEdge() {} } },
    };
    WorkflowEditor.methods.applyEdgeEdit.call(context);
    expect(context.pendingHistoryKey).to.equal('edge-1-2');
  });

  it('clears the previous immutable graph and restarts polling on route reuse', async () => {
    const context = {
      workflow: { name: 'old run' },
      details: { run: { id: 1 } },
      approvalComments: { 1: 'old' },
      pollHandle: null,
      async loadData() { this.loaded = true; },
      startPolling() { this.restarted = true; },
    };
    await WorkflowRun.methods.resetRun.call(context);
    expect(context.workflow).to.equal(null);
    expect(context.details).to.equal(null);
    expect(context.approvalComments).to.deep.equal({});
    expect(context.restarted).to.equal(true);
  });
});

describe('workflow edge panel synchronization', () => {
  it('adopts pill changes before the next edge property edit', () => {
    const context = {
      item: { nodes: [], edges: [] },
      selectedNodeId: null,
      editingEdge: {
        source_node_id: 1, destination_node_id: 2, condition: 'expression', condition_expression: 'result.successful',
      },
      history: new WorkflowHistory(50),
      pendingHistoryKey: null,
      markDirty() { this.dirty = true; },
      syncHistoryFlags: WorkflowEditor.methods.syncHistoryFlags,
    };
    WorkflowEditor.methods.onGraphChange.call(context, {
      nodes: [],
      edges: [{
        id: 3, source_node_id: 1, destination_node_id: 2, condition: 'always', condition_expression: '',
      }],
    });
    expect(context.editingEdge.condition).to.equal('always');
    expect(context.editingEdge.condition_expression).to.equal('');
  });
});
