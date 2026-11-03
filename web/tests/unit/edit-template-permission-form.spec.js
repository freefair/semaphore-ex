import './setup';
import { expect } from 'chai';
import WorkflowPermissionForm from '@/components/enhanced/WorkflowPermissionForm.vue';
import { USER_PERMISSIONS } from '@/lib/constants';

describe('workflow grant permission editor', () => {
  it('grants visibility with a workflow verb using the stable EX bit positions', () => {
    const form = { item: { permissions: 0 } };
    WorkflowPermissionForm.methods.setPermission.call(form, USER_PERMISSIONS.runWorkflows, true);
    expect(form.item.permissions).to.equal(1048704);
    expect(form.item.permissions & USER_PERMISSIONS.viewProjectResources).to.equal(0);
    expect(form.item.permissions & USER_PERMISSIONS.editWorkflows).to.equal(0);
  });

  it('retains read-only access after removing an action and clears hidden-object actions', () => {
    const form = { item: { permissions: 1048704 } };
    WorkflowPermissionForm.methods.setPermission.call(form, USER_PERMISSIONS.runWorkflows, false);
    expect(form.item.permissions).to.equal(1048576);
    WorkflowPermissionForm.methods.setPermission.call(
      form,
      USER_PERMISSIONS.approveWorkflows,
      true,
    );
    expect(form.item.permissions).to.equal(3145728);
    WorkflowPermissionForm.methods.setPermission.call(form, USER_PERMISSIONS.readProject, false);
    expect(form.item.permissions).to.equal(0);
  });
});
