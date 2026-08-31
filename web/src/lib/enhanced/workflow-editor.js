import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import { USER_PERMISSIONS, USER_ROLES } from '@/lib/constants';
import { validateWorkflowDefinition, WORKFLOW_DEFINITION_VERSION } from '@/lib/workflowValidation';

export const enhancedComputed = {
  canAdminister() {
    if (this.isNew) return this.can(USER_PERMISSIONS.administerWorkflows);
    return this.item?.effective_access?.administer
        ?? this.can(USER_PERMISSIONS.administerWorkflows);
  },
  workflowRoleOptions() {
    const builtIns = USER_ROLES.map((role) => ({
      value: `builtin:${role.slug}`,
      text: role.name,
      permissions: role.permissions,
    }));
    const custom = this.projectRoles.map((role) => ({
      value: `role:${role.id}`,
      text: role.name,
      permissions: role.permissions || 0,
    }));
    return [...builtIns, ...custom];
  },
  approvalRoleModeOptions() {
    return [
      { value: 'any_of', text: this.$t('workflowApprovalRoleModeAnyOf') },
      { value: 'all_of', text: this.$t('workflowApprovalRoleModeAllOf') },
    ];
  },
  joinOptions() {
    return [
      { value: 'all-successful', text: this.$t('workflowJoinAllSuccessful') },
      { value: 'all-complete', text: this.$t('workflowJoinAllComplete') },
      { value: 'any-successful', text: this.$t('workflowJoinAnySuccessful') },
    ];
  },
  approvalTimeoutOutcomeOptions() {
    return [
      { value: 'reject', text: this.$t('workflowApprovalTimeoutReject') },
      { value: 'approve', text: this.$t('workflowApprovalTimeoutApprove') },
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
      access_policy: {
        revision: value.access_policy?.revision || 0,
        view_role_ids: Array.isArray(value.access_policy?.view_role_ids)
          ? value.access_policy.view_role_ids : [],
        start_role_ids: Array.isArray(value.access_policy?.start_role_ids)
          ? value.access_policy.start_role_ids : [],
      },
      parameters: Array.isArray(value.parameters) ? value.parameters : [],
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
        override_policy: node.override_policy || {},
        approval_permission: node.kind === 'approval'
          ? (node.approval_permission || USER_PERMISSIONS.runProjectTasks)
          : node.approval_permission,
        approval_timeout_outcome: node.kind === 'approval'
          ? (node.approval_timeout_outcome || 'reject')
          : node.approval_timeout_outcome,
        approval_separation_of_duties: node.approval_separation_of_duties || false,
        approval_role_policy: node.kind === 'approval'
          ? {
            revision: node.approval_role_policy?.revision || 0,
            mode: node.approval_role_policy?.mode || 'any_of',
            role_ids: Array.isArray(node.approval_role_policy?.role_ids)
              ? node.approval_role_policy.role_ids : [],
            minimum_distinct_approvers:
                node.approval_role_policy?.minimum_distinct_approvers || 1,
            initiator_separation:
                node.approval_role_policy?.initiator_separation
                  ?? node.approval_separation_of_duties
                  ?? false,
          }
          : node.approval_role_policy,
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
  defaultApprovalRolePolicy() {
    return {
      revision: 0,
      mode: 'any_of',
      role_ids: this.workflowRoleOptions
        .filter((role) => (role.permissions & USER_PERMISSIONS.runProjectTasks)
            === USER_PERMISSIONS.runProjectTasks)
        .map((role) => role.value),
      minimum_distinct_approvers: 1,
      initiator_separation: false,
    };
  },
  onApprovalPolicyChanged() {
    const policy = this.editingNode.approval_role_policy;
    if (policy.mode === 'all_of') {
      policy.minimum_distinct_approvers = Math.max(
        policy.minimum_distinct_approvers || 1,
        policy.role_ids.length,
      );
    } else {
      policy.minimum_distinct_approvers = Math.max(
        policy.minimum_distinct_approvers || 1,
        1,
      );
    }
    this.editingNode.approval_separation_of_duties = policy.initiator_separation;
    this.applyNodeEdit();
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
    delete payload.effective_access;
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
