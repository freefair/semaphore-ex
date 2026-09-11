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
  canManageCrossProjectTemplates() {
    return this.can(USER_PERMISSIONS.manageProjectResources);
  },
  workflowTemplateChoices() {
    const local = (this.templates || []).map((template) => ({
      value: `local:${template.id}`,
      text: template.name,
      disabled: false,
    }));
    const shared = this.crossProjectReferences.map((reference) => ({
      value: reference.choice_value,
      text: reference.available
        ? `${reference.name} · v${reference.template_version_number}`
        : `${this.$t('crossProjectReferenceUnavailableShort')} · #${reference.template_id}`
            + ` · v${reference.template_version_number}`,
      disabled: !reference.available,
    }));
    return [...local, ...shared];
  },
  workflowGraphTemplates() {
    const byID = new Map((this.templates || []).map((template) => [template.id, template]));
    this.crossProjectReferences.forEach((reference) => {
      if (!byID.has(reference.template_id)) {
        byID.set(reference.template_id, {
          id: reference.template_id,
          name: reference.name || `${this.$t('crossProjectTemplate')} #${reference.template_id}`,
        });
      }
    });
    return [...byID.values()];
  },
  editingNodeTemplateChoice: {
    get() {
      if (!this.editingNode) return null;
      const reference = this.editingNode.cross_project_template_reference;
      if (reference) {
        return `grant:${reference.grant_id}:version:${reference.template_version_number}`;
      }
      return this.editingNode.template_id ? `local:${this.editingNode.template_id}` : null;
    },
    set(value) {
      this.applyTemplateChoice(value);
    },
  },
  editingNodeCrossProjectReference() {
    if (!this.editingNode?.cross_project_template_reference) return null;
    const current = this.editingNode.cross_project_template_reference;
    return this.crossProjectReferences.find((reference) => reference.grant_id === current.grant_id
        && reference.template_version_number === current.template_version_number) || null;
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
      this.workflowGraphTemplates.map((template) => template.id),
    );
  },
  versionMessageTooLong() {
    return new TextEncoder().encode(this.item?.version_message || '').length > 512;
  },
};

export const enhancedMethods = {
  updateArtifactOutputField(index, field, value) {
    this.$set(this.editingNode.artifact_outputs[index], field, value);
    this.applyNodeEdit();
  },
  updateArtifactInputField(index, field, value) {
    this.$set(this.editingNode.artifact_inputs[index], field, value);
    this.applyNodeEdit();
  },
  updateApprovalPolicyField(field, value) {
    this.$set(this.editingNode.approval_role_policy, field, value);
  },
  clone(value) {
    return JSON.parse(JSON.stringify(value));
  },
  prepareItem(value) {
    const item = {
      ...value,
      definition_version: value.definition_version || WORKFLOW_DEFINITION_VERSION,
      revision: value.revision || 0,
      max_parallel_tasks: value.max_parallel_tasks ?? 4,
      version_message: value.version_message || '',
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
  applyTemplateChoice(value) {
    if (!this.editingNode || !value) return;
    if (value.startsWith('local:')) {
      this.editingNode.template_id = Number(value.slice('local:'.length));
      if (this.$delete) {
        this.$delete(this.editingNode, 'cross_project_template_reference');
      } else {
        delete this.editingNode.cross_project_template_reference;
      }
    } else {
      const reference = this.crossProjectReferences.find(
        (entry) => entry.choice_value === value && entry.available !== false,
      );
      if (!reference) return;
      this.editingNode.template_id = reference.template_id;
      this.editingNode.cross_project_template_reference = {
        grant_id: reference.grant_id,
        template_version_number: reference.template_version_number,
      };
      this.editingNode.task_params = {};
      this.editingNode.override_policy = {};
    }
    this.applyNodeEdit();
  },
  includePersistedCrossProjectReferences() {
    const known = new Set(this.crossProjectReferences.map((reference) => reference.choice_value));
    (this.item?.nodes || []).forEach((node) => {
      const reference = node.cross_project_template_reference;
      if (!reference) return;
      const choice = `grant:${reference.grant_id}:version:${reference.template_version_number}`;
      if (known.has(choice)) return;
      this.crossProjectReferences.push({
        choice_value: choice,
        grant_id: reference.grant_id,
        template_id: node.template_id,
        template_version_number: reference.template_version_number,
        name: `${this.$t('crossProjectTemplate')} #${node.template_id}`,
        available: false,
      });
      known.add(choice);
    });
  },
  async refreshCrossProjectReferences() {
    if (!this.canManageCrossProjectTemplates) return;
    this.crossProjectReferencesLoading = true;
    try {
      const grantsResponse = await axios.get(
        `/api/project/${this.projectId}/cross-project-template-grants?count=100`,
      );
      const grants = (grantsResponse.data || []).filter((grant) => grant.status === 'active'
          && grant.consumer_project_id === this.projectId && (grant.operations & 1) !== 0);
      const responses = await Promise.allSettled(grants.map((grant) => axios.get(
        `/api/project/${this.projectId}/cross-project-template-grants/${grant.id}`
            + '/references?count=100',
      )));
      this.crossProjectReferences = responses.flatMap((response) => (
        response.status === 'fulfilled' ? response.value.data : []
      )).map((reference) => ({
        ...reference,
        choice_value: `grant:${reference.grant_id}`
            + `:version:${reference.template_version_number}`,
        available: true,
      }));
      this.includePersistedCrossProjectReferences();
    } catch (err) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      this.includePersistedCrossProjectReferences();
    } finally {
      this.crossProjectReferencesLoading = false;
    }
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
    payload.nodes = (payload.nodes || []).map((node) => {
      const reference = node.cross_project_template_reference;
      if (!reference) return node;
      return {
        ...node,
        cross_project_template_reference: {
          grant_id: reference.grant_id,
          template_version_number: reference.template_version_number,
        },
      };
    });
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
  onVersionRestored(restored) {
    this.item = this.prepareItem(restored);
    this.includePersistedCrossProjectReferences();
    this.autoLayout();
    this.baseline = this.clone(this.item);
    this.resetEditorState();
    this.dirty = false;
    this.graphKey += 1;
    EventBus.$emit('i-snackbar', {
      color: 'success', text: this.$t('workflowVersionRestoredSuccess'),
    });
  },
};
