import { expect } from 'chai';
import WorkflowGraph from '@/components/WorkflowGraph.vue';
import WorkflowEditor from '@/views/project/WorkflowEditor.vue';
import WorkflowEdgeProperties from '@/components/workflow/WorkflowEdgeProperties.vue';

describe('workflow edge input compatibility', () => {
  it('keeps EX edge identity and conditions when toggling explicit mappings', () => {
    const context = {
      edgeMetadata: { '1->2': { id: 17, condition_expression: 'result.successful', label: 'release' } },
      inputs: { '1->2': { input_mode: 'explicit', input_mappings: [{ var: 'release', key: 'tag' }] } },
      conditions: {},
      condKey: (a, b) => `${a}->${b}`,
      storeInputs: WorkflowGraph.methods.storeInputs,
      emitChange() {},
    };
    WorkflowGraph.methods.syncEdge.call(context, {
      source_node_id: 1, destination_node_id: 2, condition: 'expression', input_mode: 'by_name',
    });
    expect(context.inputs['1->2']).to.equal(undefined);
    expect(context.edgeMetadata['1->2']).to.include({ id: 17, label: 'release', condition_expression: 'result.successful' });
  });
  it('offers EX expression conditions beside upstream input mappings', () => {
    const choices = WorkflowEdgeProperties.computed.conditionOptions.call({ $t: (key) => key });
    expect(choices.map((c) => c.value)).to.include('expression');
    expect(WorkflowEditor.components.WorkflowEdgeProperties).to.equal(WorkflowEdgeProperties);
  });
  it('loads hints from the finished EX version and excludes active runs', async () => {
    const requested = [];
    const context = {
      item: { revision_id: 9 },
      projectId: 1,
      workflowId: 2,
      async loadEndpoint(url) {
        requested.push(url);
        return { nodes: [{ node: { id: 12 }, task: { artifacts: '{"values":{"tag":"1.2"}}' } }] };
      },
    };
    await WorkflowEditor.methods.loadOutputHints.call(context, [
      {
        id: 40, workflow_version_id: 9, revision_id: 9, status: 'succeeded',
      },
      {
        id: 41, workflow_version_id: 9, revision_id: 9, status: 'queued',
      },
      {
        id: 42, workflow_version_id: 8, revision_id: 8, status: 'succeeded',
      },
    ]);
    expect(requested).to.deep.equal(['/api/project/1/workflows/2/runs/40']);
    expect(context.outputHints.byNode[12].keys).to.deep.equal(['tag']);
  });
  it('clears an EX expression when a simple condition is chosen', () => {
    let emitted;
    WorkflowEdgeProperties.methods.emitChange.call({
      edge: { condition: 'on_success', condition_expression: 'result.successful' },
      $emit: (event, edge) => { emitted = edge; },
    });
    expect(emitted.condition_expression || '').to.equal('');
  });
  it('does not offer input injection for an owner-controlled shared template', () => {
    const template = WorkflowEdgeProperties.computed.destinationTemplate.call({
      destinationNode: { kind: 'task', template_id: 1, cross_project_template_reference: { grant_id: 2 } },
      templates: [{ id: 1 }],
    });
    expect(template).to.equal(null);
  });
  it('does not let a late hint response overwrite a different workflow', async () => {
    const context = {
      item: { revision_id: 9 },
      projectId: 1,
      workflowId: 2,
      async loadEndpoint() {
        this.workflowId = 3;
        this.item = { revision_id: 10 };
        return { nodes: [{ node: { id: 12 }, task: { artifacts: '{"values":{"tag":"old"}}' } }] };
      },
    };
    await WorkflowEditor.methods.loadOutputHints.call(context, [{ id: 40, revision_id: 9, status: 'succeeded' }]);
    expect(context.outputHints).to.equal(null);
  });
  it('keeps the active run count when optional hints fail', async () => {
    const context = {
      item: { revision_id: 9 },
      projectId: 1,
      workflowId: 2,
      async loadEndpoint() { return [{ id: 41, revision_id: 9, status: 'queued' }]; },
      async loadOutputHints() { throw new Error('hint unavailable'); },
    };
    await WorkflowEditor.methods.loadActiveRuns.call(context);
    expect(context.activeRuns).to.equal(1);
    expect(context.outputHints).to.equal(null);
  });
  it('excludes declared artifact aliases from public survey targets', () => {
    const variables = WorkflowEdgeProperties.computed.surveyVars.call({
      destinationTemplate: { survey_vars: [{ name: 'public' }, { name: 'artifact' }, { name: 'secret', type: 'secret' }] },
      destinationNode: { artifact_inputs: [{ name: 'artifact' }] },
    });
    expect(variables.map((v) => v.name)).to.deep.equal(['public']);
  });
  it('exposes stale mappings individually without hiding valid mappings', () => {
    const stale = WorkflowEdgeProperties.computed.unavailableMappings.call({
      surveyVars: [{ name: 'public' }],
      edge: { input_mappings: [{ var: 'removed', key: 'old' }, { var: 'public', key: 'tag' }] },
    });
    expect(stale).to.deep.equal([{ var: 'removed', key: 'old' }]);
  });
  it('keeps cross-project stale mappings available for manual removal', () => {
    const stale = WorkflowEdgeProperties.computed.unavailableMappings.call({
      surveyVars: [], edge: { input_mappings: [{ var: 'public', key: 'tag' }] },
    });
    expect(stale).to.have.length(1);
  });
  it('uses the immutable EX version identity instead of the revision ordinal', async () => {
    const requested = [];
    const context = {
      item: { revision: 4, current_version_id: 17 },
      projectId: 1,
      workflowId: 2,
      async loadEndpoint(url) {
        requested.push(url);
        return { nodes: [{ node: { id: 12 }, task: { artifacts: '{"values":{"tag":"1.2"}}' } }] };
      },
    };
    await WorkflowEditor.methods.loadOutputHints.call(context, [
      {
        id: 40, revision_id: 4, workflow_version_id: 4, status: 'succeeded',
      },
      {
        id: 41, revision_id: 17, workflow_version_id: 17, status: 'succeeded',
      },
    ]);
    expect(requested).to.deep.equal(['/api/project/1/workflows/2/runs/41']);
    expect(context.outputHints.byNode[12].keys).to.deep.equal(['tag']);
  });
  it('discards hints when the EX immutable version changes during the request', async () => {
    const context = {
      item: { revision: 4, current_version_id: 17 },
      projectId: 1,
      workflowId: 2,
      async loadEndpoint() {
        this.item = { revision: 5, current_version_id: 18 };
        return { nodes: [{ node: { id: 12 }, task: { artifacts: '{"values":{"tag":"old"}}' } }] };
      },
    };
    await WorkflowEditor.methods.loadOutputHints.call(context, [
      {
        id: 41, workflow_version_id: 17, revision_id: 17, status: 'succeeded',
      },
    ]);
    expect(context.outputHints).to.equal(null);
  });
});
