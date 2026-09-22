import { expect } from 'chai';
import {
  GLOBAL_PERMISSIONS,
  ROLE_PERMISSIONS,
  USER_PERMISSIONS,
  USER_ROLES,
} from '@/lib/constants';

function descriptor(permission, label, color) {
  return {
    permission,
    label,
    color,
    textColor: 'white',
  };
}

describe('Enhanced permission constants', () => {
  it('preserves the public permission values and descriptor order', () => {
    expect(USER_PERMISSIONS).to.deep.equal({
      runProjectTasks: 1,
      updateProject: 2,
      manageProjectResources: 4,
      manageProjectUsers: 8,
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
      readTaskGroups: 32768,
      createTaskGroups: 65536,
      updateTaskGroups: 131072,
      deleteTaskGroups: 262144,
      shareTaskGroups: 524288,
    });
    expect(GLOBAL_PERMISSIONS).to.deep.equal({
      manageUsers: 1,
      manageRoles: 2,
      manageSystem: 4,
      readAudit: 8,
      manageCredentialMetadata: 16,
      rotateCredentials: 32,
      grantCredentials: 64,
      managePolicyGuardrails: 128,
      rollbackPolicyGuardrails: 256,
    });
    expect(USER_ROLES).to.deep.equal([
      { slug: 'owner', name: 'Owner', permissions: 1048575 },
      { slug: 'manager', name: 'Manager', permissions: 1048565 },
      { slug: 'task_runner', name: 'Task Runner', permissions: 36273 },
      { slug: 'guest', name: 'Guest', permissions: 32816 },
    ]);
    expect(ROLE_PERMISSIONS.default).to.deep.equal([
      descriptor(1, 'canRunProjectTasks', 'blue'),
      descriptor(2, 'canUpdateProject', 'green'),
      descriptor(4, 'canManageProjectResources', 'orange'),
      descriptor(8, 'canManageProjectUsers', 'red'),
      descriptor(16, 'canViewProjectResources', 'purple'),
      descriptor(1024, 'List granted credential metadata', 'teal'),
      descriptor(2048, 'Consume granted credentials', 'indigo'),
      descriptor(4096, 'Override deployment windows', 'orange'),
      descriptor(8192, 'Manage policy guardrails', 'deep-purple'),
      descriptor(16384, 'Rollback policy guardrails', 'red darken-1'),
      descriptor(32768, 'Read task groups', 'cyan'),
      descriptor(65536, 'Create task groups', 'blue darken-2'),
      descriptor(131072, 'Update task groups', 'green darken-1'),
      descriptor(262144, 'Delete task groups', 'red'),
      descriptor(524288, 'Share task groups', 'purple darken-1'),
    ]);
    expect(ROLE_PERMISSIONS.global).to.deep.equal([
      descriptor(1, 'Manage global users', 'blue'),
      descriptor(2, 'Manage global roles', 'purple'),
      descriptor(4, 'Manage system settings', 'orange'),
      descriptor(8, 'Read global audit log', 'green'),
      descriptor(16, 'Manage global credential metadata', 'teal'),
      descriptor(32, 'Rotate global credentials', 'indigo'),
      descriptor(64, 'Grant global credentials', 'cyan darken-2'),
      descriptor(128, 'Manage global policy guardrails', 'deep-purple'),
      descriptor(256, 'Rollback global policy guardrails', 'red darken-1'),
    ]);
    expect(ROLE_PERMISSIONS.template).to.deep.equal([
      descriptor(1, 'Read template', 'purple'),
      descriptor(2, 'Run template', 'blue'),
      descriptor(4, 'Edit template', 'orange'),
      descriptor(8, 'Delete template', 'red'),
    ]);
  });

  it('maps task-group permissions to the same built-in roles as the API', () => {
    const groups = {
      read: USER_PERMISSIONS.readTaskGroups,
      create: USER_PERMISSIONS.createTaskGroups,
      update: USER_PERMISSIONS.updateTaskGroups,
      delete: USER_PERMISSIONS.deleteTaskGroups,
      share: USER_PERMISSIONS.shareTaskGroups,
    };
    const manageGroups = groups.read | groups.create | groups.update
      | groups.delete | groups.share;
    const rolePermissions = Object.fromEntries(
      USER_ROLES.map((role) => [role.slug, role.permissions]),
    );

    expect(rolePermissions.owner & manageGroups).to.equal(manageGroups);
    expect(rolePermissions.manager & manageGroups).to.equal(manageGroups);
    expect(rolePermissions.task_runner & manageGroups).to.equal(groups.read);
    expect(rolePermissions.guest & manageGroups).to.equal(groups.read);
  });
});
