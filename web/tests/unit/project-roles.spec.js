import { expect } from 'chai';
import { mount } from '@vue/test-utils';
import RolePermissionMatrix from '@/components/enhanced/RolePermissionMatrix.vue';
import axios from 'axios';
import EditRoleForm from '@/components/EditRoleForm.vue';
import Roles from '@/views/Roles.vue';
import Team from '@/views/project/Team.vue';
import { USER_ROLES } from '@/lib/constants';

describe('custom project roles', () => {
  it('starts global and project roles with an empty permission ID list', () => {
    expect(EditRoleForm.methods.getNewItem.call({ projectId: null })).to.deep.equal({
      name: '',
      slug: '',
      permission_ids: [],
      global_permissions: 0,
    });
    expect(EditRoleForm.methods.getNewItem.call({ projectId: 7 })).to.deep.equal({
      name: '',
      permission_ids: [],
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

  it('edits project permissions as IDs in the resource matrix', async () => {
    const catalog = [
      { id: 'project.read', description: 'Read project' },
      { id: 'project.templates.view', description: 'View templates' },
      { id: 'project.templates.update', description: 'Update templates' },
      { id: 'project.access_keys.use', description: 'Use access keys' },
    ];
    const wrapper = mount(RolePermissionMatrix, {
      props: { catalog, modelValue: ['project.templates.view'] },
      global: { mocks: { $t: (key) => key } },
    });
    expect(wrapper.find('[data-testid="role-matrix-templates"]').exists()).to.equal(true);
    expect(wrapper.find('[data-testid="role-matrix-access_keys"]').exists()).to.equal(true);
    wrapper.vm.toggle('project.templates.update', true);
    expect(wrapper.emitted('update:modelValue')[0][0]).to.deep.equal([
      'project.templates.view', 'project.templates.update',
    ]);
    wrapper.vm.toggle('project.templates.view', false);
    expect(wrapper.emitted('update:modelValue')[1][0]).to.deep.equal([]);
    expect(wrapper.text()).to.include('Read project');
  });

  it('exposes a custom role\'s permissions to the member summary', () => {
    const custom = {
      id: 'role_0123456789abcdef0123456789abcdef',
      name: 'Resource viewer',
      slug: 'resource-viewer',
      permission_ids: ['project.templates.view', 'project.inventories.view'],
    };
    const roles = [...USER_ROLES, custom];
    const ids = Team.methods.memberPermissionIds.call({ roles }, { roles: [custom.slug] });
    expect(ids).to.deep.equal(
      ['project.templates.view', 'project.inventories.view'],
    );
  });

  it('restores the authoritative membership after a rejected assignment', async () => {
    const previousAdapter = axios.defaults.adapter;
    let reloads = 0;
    const context = {
      projectId: 1,
      saving: Promise.resolve(),
      pendingSaves: 0,
      rowStatus: {},
      setRowStatus: Team.methods.setRowStatus,
      $t: (key) => key,
      loadItems: async () => { reloads += 1; },
    };

    axios.defaults.adapter = async () => {
      const error = new Error('Request failed with status code 409');
      error.response = { data: { message: 'last administrator' } };
      throw error;
    };

    try {
      await Team.methods.updateProjectUser.call(context, { id: 1, roles: ['guest'], revision: 1 });
    } finally {
      axios.defaults.adapter = previousAdapter;
    }

    expect(reloads).to.equal(1);
    expect(context.rowStatus[1]).to.deep.equal({ type: 'error', text: 'last administrator' });
  });
});
