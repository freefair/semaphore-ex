import { expect } from 'chai';
import axios from 'axios';
import WorkflowEditor from '@/views/project/WorkflowEditor.vue';
import WorkflowGraph from '@/components/WorkflowGraph.vue';

describe('workflow editor authoring lifecycle', () => {
  let originalPost;
  let originalPut;

  beforeEach(() => {
    originalPost = axios.post;
    originalPut = axios.put;
  });

  afterEach(() => {
    axios.post = originalPost;
    axios.put = originalPut;
  });

  it('starts versioned definitions and restores the saved baseline on discard', () => {
    const baseline = {
      name: 'Deploy', definition_version: 1, revision: 3, nodes: [], edges: [],
    };
    const context = {
      item: { ...baseline, name: 'Unsaved' },
      baseline,
      dirty: true,
      graphKey: 4,
      clone: WorkflowEditor.methods.clone,
      resetEditorState() { this.validationState = 'idle'; },
    };

    WorkflowEditor.methods.discard.call(context);

    expect(context.item).to.deep.equal(baseline);
    expect(context.dirty).to.equal(false);
    expect(context.graphKey).to.equal(5);
    expect(WorkflowEditor.methods.getNewItem().definition_version).to.equal(1);
    expect(WorkflowEditor.methods.getNewItem().max_parallel_tasks).to.equal(4);
  });

  it('defaults and preserves conditional workflow authoring fields', () => {
    const prepared = WorkflowEditor.methods.prepareItem({
      name: 'Conditional',
      nodes: [
        {
          id: 1, kind: 'task', convergence_mode: 'any', template_id: 7,
        },
        {
          id: 2, kind: 'task', template_id: 8, join_mode: 'all-complete',
        },
      ],
      edges: [{
        id: 3,
        source_node_id: 1,
        destination_node_id: 2,
        condition: 'expression',
        condition_expression: 'result.successful',
      }],
    });

    expect(prepared.max_parallel_tasks).to.equal(4);
    expect(prepared.nodes[0].join_mode).to.equal('any-successful');
    expect(prepared.nodes[1].join_mode).to.equal('all-complete');
    expect(prepared.edges[0].condition_expression).to.equal('result.successful');
  });

  it('uses the authoritative validation endpoint and retains located issues', async () => {
    let request;
    axios.post = async (url, data) => {
      request = { url, data };
      return {
        data: {
          valid: false,
          issues: [{ code: 'WORKFLOW_DISCONNECTED', path: 'nodes' }],
        },
      };
    };
    const context = {
      projectId: 7,
      item: {
        name: 'Deploy', definition_version: 1, nodes: [], edges: [],
      },
      validating: false,
      validationIssues: [],
      validationState: 'idle',
      clone: WorkflowEditor.methods.clone,
      payload: WorkflowEditor.methods.payload,
      $t: (key) => key,
    };

    const valid = await WorkflowEditor.methods.validate.call(context, false);

    expect(valid).to.equal(false);
    expect(request.url).to.equal('/api/project/7/workflows/validate');
    expect(context.validationState).to.equal('invalid');
    expect(context.validationIssues[0]).to.include({
      code: 'WORKFLOW_DISCONNECTED', path: 'nodes',
    });
  });

  it('keeps local edits and exposes reload recovery after a revision conflict', async () => {
    axios.put = async () => {
      const error = new Error('conflict');
      error.response = {
        status: 409,
        data: {
          code: 'WORKFLOW_REVISION_CONFLICT',
          current: {
            id: 41, name: 'Server', revision: 4, nodes: [], edges: [],
          },
        },
      };
      throw error;
    };
    const local = {
      id: 41, name: 'Local', revision: 3, nodes: [], edges: [],
    };
    const context = {
      clientIssues: [],
      conflict: null,
      isNew: false,
      item: local,
      projectId: 7,
      workflowId: 41,
      saving: false,
      validationState: 'idle',
      clone: WorkflowEditor.methods.clone,
      payload: WorkflowEditor.methods.payload,
      async validate() { return true; },
      $t: (key) => key,
    };

    await WorkflowEditor.methods.save.call(context);

    expect(context.item).to.equal(local);
    expect(context.conflict.code).to.equal('WORKFLOW_REVISION_CONFLICT');
    expect(context.conflict.current.revision).to.equal(4);
    expect(context.saving).to.equal(false);
  });

  it('clears temporary selections after adopting server-assigned IDs', async () => {
    axios.post = async () => ({
      data: {
        id: 41,
        name: 'Deploy',
        definition_version: 1,
        revision: 1,
        nodes: [{ id: 101, kind: 'task', template_id: 7 }],
        edges: [],
      },
    });
    const context = {
      baseline: null,
      clientIssues: [],
      dirty: true,
      editingEdge: null,
      editingNode: { id: -1 },
      graphKey: 0,
      isNew: true,
      item: {
        name: 'Deploy',
        definition_version: 1,
        revision: 0,
        nodes: [{ id: -1, kind: 'task', template_id: 7 }],
        edges: [],
      },
      projectId: 7,
      saving: false,
      selectedNodeId: -1,
      skipNextRouteReload: false,
      validationState: 'idle',
      workflowId: null,
      clone: WorkflowEditor.methods.clone,
      clearSelection: WorkflowEditor.methods.clearSelection,
      payload: WorkflowEditor.methods.payload,
      prepareItem: WorkflowEditor.methods.prepareItem,
      async validate() { return true; },
      $router: { replace() {} },
      $t: (key) => key,
    };

    await WorkflowEditor.methods.save.call(context);

    expect(context.item.nodes[0].id).to.equal(101);
    expect(context.selectedNodeId).to.equal(null);
    expect(context.editingNode).to.equal(null);
  });

  it('adopts the newer server graph only when conflict recovery is chosen', async () => {
    const context = {
      conflict: {
        current: {
          id: 41, name: 'Server', definition_version: 1, revision: 4, nodes: [], edges: [],
        },
      },
      item: { id: 41, name: 'Local', revision: 3 },
      graphKey: 2,
      dirty: true,
      clone: WorkflowEditor.methods.clone,
      prepareItem: WorkflowEditor.methods.prepareItem,
      autoLayout() {},
      resetEditorState() { this.conflict = null; },
    };

    await WorkflowEditor.methods.reloadAfterConflict.call(context);

    expect(context.item.name).to.equal('Server');
    expect(context.item.revision).to.equal(4);
    expect(context.baseline.name).to.equal('Server');
    expect(context.dirty).to.equal(false);
    expect(context.graphKey).to.equal(3);
  });

  it('allocates negative temporary node and edge IDs without colliding with persisted IDs', () => {
    const context = {
      editor: {
        export: () => ({
          drawflow: {
            Home: {
              data: {
                1: { data: { nodeId: 18 } },
                2: { data: { nodeId: -1 } },
              },
            },
          },
        }),
      },
      edgeMetadata: {
        '18->-1': { id: 22 },
        '-1->-2': { id: -3 },
      },
    };

    expect(WorkflowGraph.methods.nextNodeId.call(context)).to.equal(-2);
    expect(WorkflowGraph.methods.nextEdgeId.call(context)).to.equal(-4);
  });

  it('round-trips a condition expression through the existing graph model', () => {
    const context = {
      editor: {
        export: () => ({
          drawflow: {
            Home: {
              data: {
                1: {
                  data: { nodeId: 11, node: { kind: 'task', template_id: 7 } },
                  pos_x: 10,
                  pos_y: 20,
                  outputs: { output_1: { connections: [{ node: '2' }] } },
                },
                2: {
                  data: { nodeId: 12, node: { kind: 'task', template_id: 8 } },
                  pos_x: 30,
                  pos_y: 40,
                  outputs: { output_1: { connections: [] } },
                },
              },
            },
          },
        }),
      },
      edgeMetadata: {
        '11->12': {
          id: 21,
          condition: 'expression',
          condition_expression: 'result.summary.failed_hosts == 0',
          label: 'clean',
        },
      },
      condKey: WorkflowGraph.methods.condKey,
      stripPosition: WorkflowGraph.methods.stripPosition,
    };

    const model = WorkflowGraph.methods.exportModel.call(context);

    expect(model.edges[0]).to.include({
      condition: 'expression',
      condition_expression: 'result.summary.failed_hosts == 0',
      label: 'clean',
    });
  });
});
