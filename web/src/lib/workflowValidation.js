export const WORKFLOW_DEFINITION_VERSION = 1;
export const WORKFLOW_NODE_LIMIT = 200;
export const WORKFLOW_EDGE_LIMIT = 1000;

function issue(code, messageKey, path, nodeId = null, edgeId = null, args = {}) {
  return {
    code, messageKey, path, nodeId, edgeId, args,
  };
}

function graphHasCycle(nodes, adjacency) {
  const state = new Map();
  const visit = (node) => {
    if (state.get(node) === 1) return true;
    if (state.get(node) === 2) return false;
    state.set(node, 1);
    const cyclic = (adjacency.get(node) || []).some((next) => visit(next));
    state.set(node, 2);
    return cyclic;
  };
  return [...nodes].some((node) => visit(node));
}

export function validateWorkflowDefinition(workflow, templateIds = []) {
  const issues = [];
  const nodes = Array.isArray(workflow?.nodes) ? workflow.nodes : [];
  const edges = Array.isArray(workflow?.edges) ? workflow.edges : [];
  const allowedTemplates = new Set(templateIds);
  if (!workflow?.name?.trim()) {
    issues.push(issue('WORKFLOW_NAME_REQUIRED', 'name_required', 'name'));
  }
  const version = workflow?.definition_version || WORKFLOW_DEFINITION_VERSION;
  if (version !== WORKFLOW_DEFINITION_VERSION) {
    issues.push(issue('WORKFLOW_SCHEMA_UNSUPPORTED', 'workflowErrorSchemaUnsupported', 'definition_version'));
  }
  if (nodes.length === 0) {
    issues.push(issue('WORKFLOW_NODES_REQUIRED', 'workflowErrorNoNodes', 'nodes'));
  }
  if (nodes.length > WORKFLOW_NODE_LIMIT) {
    issues.push(issue('WORKFLOW_NODE_LIMIT_EXCEEDED', 'workflowErrorNodeLimit', 'nodes', null, null, { count: WORKFLOW_NODE_LIMIT }));
  }
  if (edges.length > WORKFLOW_EDGE_LIMIT) {
    issues.push(issue('WORKFLOW_EDGE_LIMIT_EXCEEDED', 'workflowErrorEdgeLimit', 'edges', null, null, { count: WORKFLOW_EDGE_LIMIT }));
  }

  const byId = new Map();
  const executable = new Set();
  nodes.forEach((node, index) => {
    const path = `nodes[${index}]`;
    if (!node.id) {
      issues.push(issue('WORKFLOW_NODE_ID_REQUIRED', 'workflowErrorNodeIdRequired', `${path}.id`, node.id));
    } else if (byId.has(node.id)) {
      issues.push(issue('WORKFLOW_NODE_ID_DUPLICATE', 'workflowErrorNodeIdDuplicate', `${path}.id`, node.id));
    }
    byId.set(node.id, node);
    const kind = node.kind || 'task';
    if (!['task', 'approval', 'delay', 'note'].includes(kind)) {
      issues.push(issue('WORKFLOW_NODE_KIND_INVALID', 'workflowErrorNodeKindInvalid', `${path}.kind`, node.id));
    }
    if (kind !== 'note') executable.add(node.id);
    if (kind === 'task') {
      if (!node.template_id) {
        issues.push(issue('WORKFLOW_TEMPLATE_REQUIRED', 'workflowErrorTaskNeedsTemplate', `${path}.template_id`, node.id));
      } else if (allowedTemplates.size > 0 && !allowedTemplates.has(node.template_id)) {
        issues.push(issue('WORKFLOW_TEMPLATE_NOT_IN_PROJECT', 'workflowErrorTemplateNotInProject', `${path}.template_id`, node.id));
      }
    }
    if (kind === 'delay' && (!Number.isSafeInteger(node.delay_seconds) || node.delay_seconds <= 0)) {
      issues.push(issue('WORKFLOW_DELAY_INVALID', 'workflowErrorDelayPositive', `${path}.delay_seconds`, node.id));
    }
    if (kind === 'approval' && node.approval_timeout != null && node.approval_timeout <= 0) {
      issues.push(issue('WORKFLOW_APPROVAL_TIMEOUT_INVALID', 'workflowErrorApprovalTimeoutPositive', `${path}.approval_timeout`, node.id));
    }
  });

  const edgeIds = new Set();
  const adjacency = new Map();
  const undirected = new Map();
  const incoming = new Map();
  edges.forEach((edge, index) => {
    const path = `edges[${index}]`;
    if (!edge.id) {
      issues.push(issue('WORKFLOW_EDGE_ID_REQUIRED', 'workflowErrorEdgeIdRequired', `${path}.id`, null, edge.id));
    } else if (edgeIds.has(edge.id)) {
      issues.push(issue('WORKFLOW_EDGE_ID_DUPLICATE', 'workflowErrorEdgeIdDuplicate', `${path}.id`, null, edge.id));
    }
    edgeIds.add(edge.id);
    const sourceExists = byId.has(edge.source_node_id);
    const destinationExists = byId.has(edge.destination_node_id);
    if (!sourceExists) {
      issues.push(issue('WORKFLOW_EDGE_SOURCE_MISSING', 'workflowErrorEdgeSourceMissing', `${path}.source_node_id`, null, edge.id));
    }
    if (!destinationExists) {
      issues.push(issue('WORKFLOW_EDGE_DESTINATION_MISSING', 'workflowErrorEdgeDestinationMissing', `${path}.destination_node_id`, null, edge.id));
    }
    if (edge.source_node_id === edge.destination_node_id) {
      issues.push(issue('WORKFLOW_SELF_EDGE', 'workflowSelfEdgeBlocked', path, null, edge.id));
    }
    const sourceExecutable = executable.has(edge.source_node_id);
    const destinationExecutable = executable.has(edge.destination_node_id);
    if (sourceExists && destinationExists && (!sourceExecutable || !destinationExecutable)) {
      issues.push(issue('WORKFLOW_NOTE_EDGE_FORBIDDEN', 'workflowErrorNoteEdge', path, null, edge.id));
    }
    const connectsExecutableNodes = sourceExecutable && destinationExecutable
      && edge.source_node_id !== edge.destination_node_id;
    if (connectsExecutableNodes) {
      if (!adjacency.has(edge.source_node_id)) adjacency.set(edge.source_node_id, []);
      adjacency.get(edge.source_node_id).push(edge.destination_node_id);
      if (!undirected.has(edge.source_node_id)) undirected.set(edge.source_node_id, []);
      if (!undirected.has(edge.destination_node_id)) undirected.set(edge.destination_node_id, []);
      undirected.get(edge.source_node_id).push(edge.destination_node_id);
      undirected.get(edge.destination_node_id).push(edge.source_node_id);
      incoming.set(edge.destination_node_id, (incoming.get(edge.destination_node_id) || 0) + 1);
    }
  });

  if (executable.size > 0) {
    const roots = [...executable].filter((id) => !incoming.has(id));
    if (roots.length === 0) {
      issues.push(issue('WORKFLOW_ROOT_COUNT_INVALID', 'workflowErrorNoRoot', 'nodes'));
    } else if (roots.length > 1) {
      issues.push(issue('WORKFLOW_ROOT_COUNT_INVALID', 'workflowErrorMultipleRoots', 'nodes', null, null, { count: roots.length }));
    }
    if (graphHasCycle(executable, adjacency)) {
      issues.push(issue('WORKFLOW_CYCLE', 'workflowCycleBlocked', 'edges'));
    }
    const first = executable.values().next().value;
    const seen = new Set([first]);
    const stack = [first];
    while (stack.length) {
      const current = stack.pop();
      (undirected.get(current) || []).forEach((next) => {
        if (!seen.has(next)) {
          seen.add(next);
          stack.push(next);
        }
      });
    }
    if (seen.size !== executable.size) {
      issues.push(issue('WORKFLOW_DISCONNECTED', 'workflowErrorDisconnected', 'nodes'));
    }
  }
  return issues;
}
