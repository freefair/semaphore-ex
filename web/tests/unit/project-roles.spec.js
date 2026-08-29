import { expect } from 'chai';
import axios from 'axios';
import EditRoleForm from '@/components/EditRoleForm.vue';
import Roles from '@/views/Roles.vue';
import Team from '@/views/project/Team.vue';
import EventBus from '@/event-bus';
import { USER_PERMISSIONS, USER_ROLES } from '@/lib/constants';
import canViewProjectResources from '@/lib/project-permissions';

describe('custom project roles', () => {
  it('preserves the existing global-role editor contract', () => {
    expect(EditRoleForm.methods.getNewItem.call({ projectId: null })).to.deep.equal({
      name: '',
      slug: '',
      permissions: 0,
    });
    expect(EditRoleForm.data()).to.have.nested.property(
      'permissions.canManageProjectUsers',
      false,
    );
    expect(EditRoleForm.methods.getNewItem.call({ projectId: 7 })).to.deep.equal({
      name: '',
      permissions: 0,
    });
  });

  it('keeps immutable IDs as the UI resource identity', () => {
    expect(Roles.computed.IDFieldName()).to.equal('id');
    expect(Roles.methods.getDeleteItemUrl.call({
      projectId: 7,
      itemId: 'role_0123456789abcdef0123456789abcdef',
      getSingleItemUrl: Roles.methods.getSingleItemUrl,
    }, { revision: 3 })).to.equal(
      '/api/project/7/roles/role_0123456789abcdef0123456789abcdef?revision=3',
    );
  });

  it('uses backend catalog bits without a duplicated checkbox model', () => {
    const context = { item: { permissions: 0 } };
    EditRoleForm.methods.setPermission.call(
      context,
      USER_PERMISSIONS.viewProjectResources,
      true,
    );
    expect(context.item.permissions).to.equal(USER_PERMISSIONS.viewProjectResources);
    expect(EditRoleForm.methods.hasPermission.call(
      context,
      USER_PERMISSIONS.viewProjectResources,
    )).to.equal(true);
    EditRoleForm.methods.setPermission.call(
      context,
      USER_PERMISSIONS.viewProjectResources,
      false,
    );
    expect(context.item.permissions).to.equal(0);
  });

  it('preserves built-in access while exposing custom effective permissions', () => {
    expect(USER_ROLES.every((role) => (
      role.permissions & USER_PERMISSIONS.viewProjectResources
    ) === USER_PERMISSIONS.viewProjectResources)).to.equal(true);
    const custom = {
      id: 'role_0123456789abcdef0123456789abcdef',
      name: 'Resource viewer',
      permissions: USER_PERMISSIONS.viewProjectResources,
    };
    const roles = Team.computed.userRoles.call({ roles: [custom] });
    expect(roles.find((role) => role.value === custom.id).permissions).to.equal(
      USER_PERMISSIONS.viewProjectResources,
    );
    expect(Team.methods.rolePermissions.call({ userRoles: roles }, custom.id)).to.equal(
      USER_PERMISSIONS.viewProjectResources,
    );
  });

  it('hides repository navigation only when the backend permission is absent', () => {
    expect(canViewProjectResources(
      { admin: false },
      { permissions: USER_PERMISSIONS.viewProjectResources },
    )).to.equal(true);
    expect(canViewProjectResources({ admin: false }, { permissions: 0 })).to.equal(false);
  });

  it('restores the authoritative membership after a rejected assignment', async () => {
    const previousAdapter = axios.defaults.adapter;
    let reloads = 0;
    let snackbar;
    const captureSnackbar = (message) => { snackbar = message; };
    EventBus.$once('i-snackbar', captureSnackbar);

    axios.defaults.adapter = async () => {
      const error = new Error('Request failed with status code 409');
      error.response = { data: { message: 'last administrator' } };
      throw error;
    };

    try {
      await Team.methods.updateProjectUser.call({
        projectId: 1,
        loadItems: async () => { reloads += 1; },
      }, { id: 1, role: 'guest' });
    } finally {
      axios.defaults.adapter = previousAdapter;
      EventBus.$off('i-snackbar', captureSnackbar);
    }

    expect(reloads).to.equal(1);
    expect(snackbar).to.deep.equal({ color: 'error', text: 'last administrator' });
  });
});
