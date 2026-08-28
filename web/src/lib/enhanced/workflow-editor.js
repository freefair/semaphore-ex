import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import { validateWorkflowDefinition, WORKFLOW_DEFINITION_VERSION } from '@/lib/workflowValidation';

export const enhancedComputed = {
  joinOptions() {
    return [
      { value: 'all-successful', text: this.$t('workflowJoinAllSuccessful') },
      { value: 'all-complete', text: this.$t('workflowJoinAllComplete') },
      { value: 'any-successful', text: this.$t('workflowJoinAnySuccessful') },
    ];
  },
  artifactOutputTypes() {
    return [
      { value: 'string', text: this.$t('workflowArtifactTypeString') },
      { value: 'integer', text: this.$t('workflowArtifactTypeInteger') },
      { value: 'number', text: this.$t('workflowArtifactTypeNumber') },
      { value: 'boolean', text: this.$t('workflowArtifactTypeBoolean') },
      { value: 'object', text: this.$t('workflowArtifactTypeObject') },
      { value: 'array', text: this.$t('workflowArtifactTypeArray') },
    ];
  },
  reachableArtifactOutputs() {
    if (!this.editingNode || !this.item) return [];
    const predecessors = new Set();
    const pending = [this.editingNode.id];
    while (pending.length > 0) {
      const destinationId = pending.pop();
      (this.item.edges || [])
        .filter((edge) => edge.destination_node_id === destinationId)
        .forEach((edge) => {
          if (!predecessors.has(edge.source_node_id)) {
            predecessors.add(edge.source_node_id);
            pending.push(edge.source_node_id);
          }
        });
    }
    return (this.item.nodes || [])
      .filter((node) => predecessors.has(node.id))
      .flatMap((node) => (node.artifact_outputs || []).map((output) => ({
        value: `${node.id}:${output.name}`,
        sourceNodeId: node.id,
        output: output.name,
        text: `#${node.id} ${node.display_name || ''} · ${output.name} (${output.schema.type})`,
      })));
  },
  clientIssues() {
    return validateWorkflowDefinition(
      this.item,
      (this.templates || []).map((template) => template.id),
    );
  },
};

export const enhancedMethods = {
  clone(value) {
    return JSON.parse(JSON.stringify(value));
  },
  prepareItem(value) {
    const item = {
      ...value,
      definition_version: value.definition_version || WORKFLOW_DEFINITION_VERSION,
      revision: value.revision || 0,
      max_parallel_tasks: value.max_parallel_tasks ?? 4,
      nodes: Array.isArray(value.nodes) ? value.nodes : [],
      edges: Array.isArray(value.edges) ? value.edges : [],
    };
    item.nodes = item.nodes.map((node) => {
      const convergence = node.convergence_mode || 'all';
      return {
        kind: 'task',
        position_x: 0,
        position_y: 0,
        display_name: '',
        ...node,
        convergence_mode: convergence,
        join_mode: node.join_mode
            || (convergence === 'any' ? 'any-successful' : 'all-successful'),
        artifact_outputs: Array.isArray(node.artifact_outputs) ? node.artifact_outputs : [],
        artifact_inputs: Array.isArray(node.artifact_inputs) ? node.artifact_inputs : [],
      };
    });
    item.edges = item.edges.map((edge) => ({
      condition: 'on_success',
      condition_expression: '',
      label: '',
      ...edge,
    }));
    return item;
  },
  clearSelection() {
    this.selectedNodeId = null;
    this.editingNode = null;
    this.editingEdge = null;
  },
  resetEditorState() {
    this.clearSelection();
    this.validationIssues = [];
    this.validationState = 'idle';
    this.conflict = null;
  },
  addArtifactOutput() {
    this.editingNode.artifact_outputs.push({
      name: '',
      schema: { type: 'string' },
      sensitive: false,
      max_bytes: 16384,
    });
    this.applyNodeEdit();
  },
  removeArtifactOutput(index) {
    this.editingNode.artifact_outputs.splice(index, 1);
    this.applyNodeEdit();
  },
  setArtifactOutputType(index, type) {
    const schema = { type };
    if (type === 'object') schema.properties = {};
    if (type === 'array') schema.items = { type: 'string' };
    this.$set(this.editingNode.artifact_outputs[index], 'schema', schema);
    this.applyNodeEdit();
  },
  artifactReferenceKey(input) {
    if (!input.source_node_id || !input.output) return null;
    return `${input.source_node_id}:${input.output}`;
  },
  addArtifactInput() {
    const source = this.reachableArtifactOutputs[0];
    if (!source) return;
    this.editingNode.artifact_inputs.push({
      name: source.output,
      source_node_id: source.sourceNodeId,
      output: source.output,
      required: true,
    });
    this.applyNodeEdit();
  },
  removeArtifactInput(index) {
    this.editingNode.artifact_inputs.splice(index, 1);
    this.applyNodeEdit();
  },
  setArtifactReference(index, value) {
    const source = this.reachableArtifactOutputs.find((entry) => entry.value === value);
    if (!source) return;
    const input = this.editingNode.artifact_inputs[index];
    input.source_node_id = source.sourceNodeId;
    input.output = source.output;
    this.applyNodeEdit();
  },
  onEdgeConditionChanged() {
    if (!this.editingEdge) return;
    if (this.editingEdge.condition !== 'expression') {
      this.editingEdge.condition_expression = '';
    }
    this.applyEdgeEdit();
  },
  markDirty() {
    this.dirty = true;
    this.validationState = 'idle';
    this.validationIssues = [];
    this.conflict = null;
  },
  problemText(problem) {
    const message = problem.messageKey
      ? this.$t(problem.messageKey, problem.args || {})
      : problem.message;
    return problem.path ? `${message} (${problem.path})` : message;
  },
  payload() {
    const payload = this.clone({ ...this.item, project_id: this.projectId });
    if (!payload.start_version) delete payload.start_version;
    return payload;
  },
  async validate(showSuccess = true) {
    this.validating = true;
    try {
      const response = await axios.post(
        `/api/project/${this.projectId}/workflows/validate`,
        this.payload(),
      );
      this.validationIssues = response.data.issues || [];
      this.validationState = response.data.valid ? 'valid' : 'invalid';
      if (showSuccess && response.data.valid) {
        EventBus.$emit('i-snackbar', {
          color: 'success', text: this.$t('workflowValidationPassed'),
        });
      }
      return response.data.valid;
    } catch (err) {
      this.validationState = 'error';
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      return false;
    } finally {
      this.validating = false;
    }
  },
  discard() {
    if (!this.baseline) return;
    this.item = this.clone(this.baseline);
    this.resetEditorState();
    this.dirty = false;
    this.graphKey += 1;
  },
  async reload() {
    await this.loadData();
  },
  async reloadAfterConflict() {
    if (this.conflict?.current) {
      this.item = this.prepareItem(this.conflict.current);
      this.autoLayout();
      this.baseline = this.clone(this.item);
      this.resetEditorState();
      this.dirty = false;
      this.graphKey += 1;
      return;
    }
    await this.reload();
  },
};
