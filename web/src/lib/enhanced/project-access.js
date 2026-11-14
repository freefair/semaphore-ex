// Project permissions as the server's catalog IDs.
//
// Permission masks use bits up to 2^54. JSON numbers become IEEE doubles in
// the browser, which drop the low bits of such masks, and JavaScript bitwise
// operators work on 32 bits. The UI therefore decides only on the
// `permission_ids` lists the API returns next to every project mask.

import { USER_PERMISSIONS } from '@/lib/constants';

export const PROJECT_RESOURCES = [
  'templates',
  'inventories',
  'repositories',
  'access_keys',
  'environments',
  'schedules',
  'integrations',
  'views',
];

export const RESOURCE_ACTIONS = ['view', 'create', 'update', 'delete'];

export function resourcePermission(resource, action) {
  if (!PROJECT_RESOURCES.includes(resource) || !RESOURCE_ACTIONS.concat('use').includes(action)) {
    throw new Error(`Unknown project permission ${resource}.${action}`);
  }
  if (action === 'use' && resource !== 'access_keys') {
    throw new Error(`Unknown project permission ${resource}.${action}`);
  }
  return `project.${resource}.${action}`;
}

const ALL_VIEW_IDS = PROJECT_RESOURCES.map((resource) => resourcePermission(resource, 'view'));
const ALL_MUTATION_IDS = PROJECT_RESOURCES.flatMap((resource) => ['create', 'update', 'delete']
  .map((action) => resourcePermission(resource, action)))
  .concat(resourcePermission('access_keys', 'use'));

// Legacy numeric constants keep their historic meaning: the two retired coarse
// resource permissions require every view or every mutation permission.
const LEGACY_IDS = new Map([
  [USER_PERMISSIONS.runProjectTasks, ['project.tasks.run']],
  [USER_PERMISSIONS.updateProject, ['project.settings.update']],
  [USER_PERMISSIONS.manageProjectResources, ALL_MUTATION_IDS],
  [USER_PERMISSIONS.manageProjectUsers, ['project.members.manage']],
  [USER_PERMISSIONS.viewProjectResources, ALL_VIEW_IDS],
  [USER_PERMISSIONS.viewWorkflows, ['workflow.view']],
  [USER_PERMISSIONS.editWorkflows, ['workflow.edit']],
  [USER_PERMISSIONS.startWorkflows, ['workflow.start']],
  [USER_PERMISSIONS.stopWorkflows, ['workflow.stop']],
  [USER_PERMISSIONS.administerWorkflows, ['workflow.administer']],
  [USER_PERMISSIONS.listGrantedCredentials, ['project.credentials.granted.list']],
  [USER_PERMISSIONS.consumeGrantedCredentials, ['project.credentials.granted.consume']],
  [USER_PERMISSIONS.overrideDeploymentWindow, ['project.deployment_windows.override']],
  [USER_PERMISSIONS.managePolicyGuardrails, ['project.policy_guardrails.manage']],
  [USER_PERMISSIONS.rollbackPolicyGuardrails, ['project.policy_guardrails.rollback']],
  [USER_PERMISSIONS.readTaskGroups, ['project.task_groups.read']],
  [USER_PERMISSIONS.createTaskGroups, ['project.task_groups.create']],
  [USER_PERMISSIONS.updateTaskGroups, ['project.task_groups.update']],
  [USER_PERMISSIONS.deleteTaskGroups, ['project.task_groups.delete']],
  [USER_PERMISSIONS.shareTaskGroups, ['project.task_groups.share']],
  [USER_PERMISSIONS.readProject, ['project.read']],
  [USER_PERMISSIONS.approveWorkflows, ['workflow.approve']],
]);

// requiredPermissionIds turns a permission ID, a list of IDs, or a legacy
// numeric constant into the IDs that must all be granted. Unknown values
// require something nobody holds, so a typo never grants access.
export function requiredPermissionIds(required) {
  if (Array.isArray(required)) {
    return required.flatMap((value) => requiredPermissionIds(value));
  }
  if (typeof required === 'string') {
    return [required];
  }
  if (LEGACY_IDS.has(required)) {
    return LEGACY_IDS.get(required);
  }
  return [`unknown:${required}`];
}

// hasProjectPermission answers from the granted ID list only. Without a list
// (not loaded, or an older server) nothing is granted.
export function hasProjectPermission(grantedIds, required) {
  if (!Array.isArray(grantedIds)) {
    return false;
  }
  const granted = new Set(grantedIds);
  const needed = requiredPermissionIds(required);
  return needed.length > 0 && needed.every((id) => granted.has(id));
}

// The role form edits roles as ID lists too: the retired coarse IDs are never
// offered or sent.
export const RETIRED_PERMISSION_IDS = ['project.resources.view', 'project.resources.manage'];

export function resourceMatrix(catalog) {
  const byId = new Map((catalog || []).map((definition) => [definition.id, definition]));
  const actionsOf = (resource) => RESOURCE_ACTIONS.concat(resource === 'access_keys' ? ['use'] : []);
  const rows = PROJECT_RESOURCES
    .map((resource) => ({
      resource,
      cells: actionsOf(resource)
        .map((action) => byId.get(`project.${resource}.${action}`))
        .filter(Boolean),
    }))
    .filter((row) => row.cells.length > 0);
  const inMatrix = new Set(rows.flatMap((row) => row.cells.map((cell) => cell.id)));
  const other = (catalog || [])
    .filter((definition) => !inMatrix.has(definition.id)
      && !RETIRED_PERMISSION_IDS.includes(definition.id));
  return { rows, other };
}

export function togglePermissionId(ids, id, enabled) {
  const next = new Set(ids || []);
  if (enabled) {
    next.add(id);
  } else {
    next.delete(id);
  }
  return [...next];
}

// Labels shared by the role matrix, the member summary and the effective
// permissions view, so a permission has one name everywhere. Resource names are
// the navigation's.
export const RESOURCE_LABEL_KEYS = {
  templates: 'taskTemplates',
  inventories: 'inventory',
  repositories: 'repositories',
  access_keys: 'keyStore',
  environments: 'environment',
  schedules: 'schedule',
  integrations: 'integrations',
  views: 'permResourceViews',
};

export const ACTION_LABEL_KEYS = {
  view: 'permActionView',
  create: 'permActionCreate',
  update: 'permActionUpdate',
  delete: 'permActionDelete',
  use: 'permActionUse',
};

// permissionLabel names one permission the way the matrix and the summary
// do: "Task Templates: View" for resource permissions, the catalog
// description otherwise.
export function permissionLabel(id, catalog, t) {
  const match = /^project\.([a-z_]+)\.([a-z]+)$/.exec(id || '');
  if (match && RESOURCE_LABEL_KEYS[match[1]] && ACTION_LABEL_KEYS[match[2]]) {
    return `${t(RESOURCE_LABEL_KEYS[match[1]])}: ${t(ACTION_LABEL_KEYS[match[2]])}`;
  }
  const definition = (catalog || []).find((entry) => entry.id === id);
  return definition?.description || id;
}

// permissionSummary groups granted IDs for display: one entry per resource
// with its granted actions in catalog order, then every other granted
// permission with its catalog description. Nothing granted is left out.
export function permissionSummary(grantedIds, catalog) {
  const granted = new Set(grantedIds || []);
  const { rows, other } = resourceMatrix(catalog);
  const resources = rows
    .map((row) => ({
      resource: row.resource,
      actions: row.cells.filter((cell) => granted.has(cell.id)).map((cell) => cell.id.split('.').pop()),
    }))
    .filter((row) => row.actions.length > 0);
  const others = other.filter((definition) => granted.has(definition.id));
  const known = new Set((catalog || []).map((definition) => definition.id));
  const unknown = [...granted]
    .filter((id) => !known.has(id))
    .map((id) => ({ id, description: id }));
  return { resources, others: others.concat(unknown) };
}
