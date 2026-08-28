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
