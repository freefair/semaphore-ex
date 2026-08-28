import { expect } from 'chai';
import {
  validateWorkflowDefinition,
  WORKFLOW_DEFINITION_VERSION,
} from '@/lib/workflowValidation';

function validWorkflow() {
  return {
    name: 'Deploy',
    definition_version: WORKFLOW_DEFINITION_VERSION,
    nodes: [
      { id: -1, kind: 'task', template_id: 10 },
      { id: -2, kind: 'approval' },
    ],
    edges: [
      {
        id: -1, source_node_id: -1, destination_node_id: -2, condition: 'on_success',
      },
    ],
  };
}

describe('workflow definition validation', () => {
  it('accepts delay nodes and rejects missing or nonpositive integer durations', () => {
    const workflow = { name: 'Wait', nodes: [{ id: 1, kind: 'delay', delay_seconds: 60 }], edges: [] };
    expect(validateWorkflowDefinition(workflow)).to.deep.equal([]);
    [null, 0, -1, 1.5].forEach((seconds) => {
      workflow.nodes[0].delay_seconds = seconds;
      expect(validateWorkflowDefinition(workflow).map((entry) => entry.code)).to.include('WORKFLOW_DELAY_INVALID');
    });
  });

  it('accepts a connected acyclic graph with project templates', () => {
    expect(validateWorkflowDefinition(validWorkflow(), [10])).to.deep.equal([]);
  });

  it('reports stable codes and graph locations', () => {
    const workflow = validWorkflow();
    workflow.nodes[0].template_id = 99;
    workflow.edges[0].destination_node_id = 404;

    const issues = validateWorkflowDefinition(workflow, [10]);

    expect(issues.map((entry) => entry.code)).to.include.members([
      'WORKFLOW_TEMPLATE_NOT_IN_PROJECT',
      'WORKFLOW_EDGE_DESTINATION_MISSING',
      'WORKFLOW_DISCONNECTED',
    ]);
    expect(issues.find((entry) => entry.code === 'WORKFLOW_TEMPLATE_NOT_IN_PROJECT')).to.include({
      path: 'nodes[0].template_id', nodeId: -1,
    });
    expect(issues.find((entry) => entry.code === 'WORKFLOW_EDGE_DESTINATION_MISSING')).to.include({
      path: 'edges[0].destination_node_id', edgeId: -1,
    });
  });

  it('detects cycles, self-edges, duplicate IDs, and disconnected nodes', () => {
    const workflow = validWorkflow();
    workflow.nodes.push({ id: -3, kind: 'task', template_id: 10 });
    workflow.edges.push(
      {
        id: -1, source_node_id: -2, destination_node_id: -1, condition: 'always',
      },
      {
        id: -3, source_node_id: -3, destination_node_id: -3, condition: 'always',
      },
    );

    const codes = validateWorkflowDefinition(workflow, [10]).map((entry) => entry.code);

    expect(codes).to.include('WORKFLOW_EDGE_ID_DUPLICATE');
    expect(codes).to.include('WORKFLOW_CYCLE');
    expect(codes).to.include('WORKFLOW_SELF_EDGE');
    expect(codes).to.include('WORKFLOW_DISCONNECTED');
  });
});
