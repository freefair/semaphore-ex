export const ENHANCED_USER_PERMISSIONS = {
  viewProjectResources: 16,
  viewWorkflows: 32,
  editWorkflows: 64,
  startWorkflows: 128,
  stopWorkflows: 256,
  administerWorkflows: 512,
  listGrantedCredentials: 1024,
  consumeGrantedCredentials: 2048,
  overrideDeploymentWindow: 4096,
  managePolicyGuardrails: 8192,
  rollbackPolicyGuardrails: 16384,
};

export const GLOBAL_PERMISSIONS = {
  manageUsers: 1,
  manageRoles: 2,
  manageSystem: 4,
  readAudit: 8,
  manageCredentialMetadata: 16,
  rotateCredentials: 32,
  grantCredentials: 64,
  managePolicyGuardrails: 128,
  rollbackPolicyGuardrails: 256,
};

export const ENHANCED_PROJECT_ROLE_PERMISSIONS = [{
  permission: ENHANCED_USER_PERMISSIONS.viewProjectResources,
  label: 'canViewProjectResources',
  color: 'purple',
  textColor: 'white',
}, {
  permission: ENHANCED_USER_PERMISSIONS.listGrantedCredentials,
  label: 'List granted credential metadata',
  color: 'teal',
  textColor: 'white',
}, {
  permission: ENHANCED_USER_PERMISSIONS.consumeGrantedCredentials,
  label: 'Consume granted credentials',
  color: 'indigo',
  textColor: 'white',
}, {
  permission: ENHANCED_USER_PERMISSIONS.overrideDeploymentWindow,
  label: 'Override deployment windows',
  color: 'orange',
  textColor: 'white',
}, {
  permission: ENHANCED_USER_PERMISSIONS.managePolicyGuardrails,
  label: 'Manage policy guardrails',
  color: 'deep-purple',
  textColor: 'white',
}, {
  permission: ENHANCED_USER_PERMISSIONS.rollbackPolicyGuardrails,
  label: 'Rollback policy guardrails',
  color: 'red darken-1',
  textColor: 'white',
}];

export const ENHANCED_GLOBAL_ROLE_PERMISSIONS = [{
  permission: GLOBAL_PERMISSIONS.manageUsers,
  label: 'Manage global users',
  color: 'blue',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.manageRoles,
  label: 'Manage global roles',
  color: 'purple',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.manageSystem,
  label: 'Manage system settings',
  color: 'orange',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.readAudit,
  label: 'Read global audit log',
  color: 'green',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.manageCredentialMetadata,
  label: 'Manage global credential metadata',
  color: 'teal',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.rotateCredentials,
  label: 'Rotate global credentials',
  color: 'indigo',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.grantCredentials,
  label: 'Grant global credentials',
  color: 'cyan darken-2',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.managePolicyGuardrails,
  label: 'Manage global policy guardrails',
  color: 'deep-purple',
  textColor: 'white',
}, {
  permission: GLOBAL_PERMISSIONS.rollbackPolicyGuardrails,
  label: 'Rollback global policy guardrails',
  color: 'red darken-1',
  textColor: 'white',
}];
