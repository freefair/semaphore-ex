import './local-storage-fixture';
import { expect } from 'chai';
import EditRoleForm from '@/components/EditRoleForm.vue';
import EditTemplatePermissionForm from '@/components/EditTemplatePermissionForm.vue';
import EventBus from '@/event-bus';
import TemplatePermissionsChips from '@/components/TemplatePermissionsChips.vue';
import UserForm from '@/components/UserForm.vue';
import Roles from '@/views/Roles.vue';
import TemplateView from '@/views/project/TemplateView.vue';
import TemplatePerms from '@/views/project/template/TemplatePerms.vue';
import Users from '@/views/Users.vue';
import { GLOBAL_PERMISSIONS, USER_PERMISSIONS } from '@/lib/constants';
import {
  hasGlobalPermission,
  setTemplatePermissionEffect,
  templatePermissionEffect,
} from '@/lib/role-permissions';

describe('global and template roles', () => {
  it('checks global permissions independently and retains administrator break-glass', () => {
    const systemInfo = {
      global_permissions: { permissions: GLOBAL_PERMISSIONS.manageRoles },
    };
    expect(hasGlobalPermission(systemInfo, GLOBAL_PERMISSIONS.manageRoles)).to.equal(true);
    expect(hasGlobalPermission(systemInfo, GLOBAL_PERMISSIONS.manageUsers)).to.equal(false);
    expect(hasGlobalPermission(null, GLOBAL_PERMISSIONS.manageSystem, true)).to.equal(true);
  });

  it('edits typed global and legacy project masks independently', async () => {
    const item = { permissions: 0, global_permissions: 0 };
    EditRoleForm.methods.setPermission.call({ item, projectId: null }, 2, true);
    expect(item.global_permissions).to.equal(2);
    expect(item.permissions).to.equal(0);

    const context = {
      item,
      projectId: null,
      permissionCatalog: [],
      loadEndpoint: async (url) => [{ id: url }],
    };
    await EditRoleForm.methods.beforeLoadData.call(context);
    expect(context.permissionCatalog[0].id).to.equal('/api/roles/permissions');
    expect(EditRoleForm.methods.getNewItem.call({ projectId: null })).to.deep.equal({
      name: '',
      slug: '',
      permissions: 0,
      global_permissions: 0,
    });
  });

  it('grants global navigation and list actions only for the matching permission', () => {
    const rolesContext = {
      projectId: null,
      isAdmin: false,
      systemInfo: { global_permissions: { permissions: GLOBAL_PERMISSIONS.manageRoles } },
    };
    expect(Roles.computed.canManageRoles.call(rolesContext)).to.equal(true);

    const usersContext = {
      isAdmin: false,
      systemInfo: { global_permissions: { permissions: GLOBAL_PERMISSIONS.manageUsers } },
    };
    expect(Users.computed.canManageUsers.call(usersContext)).to.equal(true);
    expect(Users.computed.canManageGlobalRoles.call(usersContext)).to.equal(false);
  });

  it('refreshes user action headers when asynchronous global access resolves', () => {
    const allHeaders = [{ value: 'name' }, { value: 'actions' }];
    const context = {
      canManageUsers: false,
      headers: [],
      getHeaders: () => allHeaders,
      refreshHeaders: Users.methods.refreshHeaders,
    };

    Users.methods.refreshHeaders.call(context);
    expect(context.headers).to.deep.equal([{ value: 'name' }]);

    context.canManageUsers = true;
    Users.watch.canManageUsers.call(context);
    expect(context.headers).to.deep.equal(allHeaders);
  });

  it('refreshes user action headers after computed permissions initialize', () => {
    let refreshes = 0;
    Users.created.call({
      refreshHeaders() {
        refreshes += 1;
      },
    });
    expect(refreshes).to.equal(1);
  });

  it('filters already assigned global roles and bounds provenance to the deciding role', () => {
    const available = UserForm.computed.availableGlobalRoles.call({
      globalRoles: [{ id: 'operator' }, { id: 'auditor' }],
      globalRoleAssignments: [{ role_id: 'operator' }],
    });
    expect(available).to.deep.equal([{ id: 'auditor' }]);

    expect(UserForm.methods.globalPermissionProvenance.call({}, {
      allowed: true,
      provenance: { effect: 'allow', role_name: 'Auditor', scope: 'global' },
    })).to.equal('allow by Auditor (global)');
    expect(UserForm.methods.globalPermissionProvenance.call({}, {
      allowed: false,
      provenance: { scope: 'global' },
    })).to.equal('Denied at global scope');
  });

  it('keeps template overrides mutually exclusive across inherit, allow, and deny', () => {
    let item = setTemplatePermissionEffect({
      allowed_permissions: 0,
      denied_permissions: 0,
    }, 2, 'allow');
    expect(templatePermissionEffect(item, 2)).to.equal('allow');
    expect(item).to.deep.equal({ allowed_permissions: 2, denied_permissions: 0 });

    const form = { item };
    EditTemplatePermissionForm.methods.setPermissionEffect.call(form, 2, 'deny');
    item = form.item;
    expect(EditTemplatePermissionForm.methods.permissionEffect.call(form, 2)).to.equal('deny');
    expect(item).to.deep.equal({ allowed_permissions: 0, denied_permissions: 2 });

    item = setTemplatePermissionEffect(item, 2, 'inherit');
    expect(item).to.deep.equal({ allowed_permissions: 0, denied_permissions: 0 });
  });

  it('renders explicit scoped permission labels without i18n lookups', () => {
    let translations = 0;
    const context = {
      scope: 'template',
      $t: () => { translations += 1; },
    };

    expect(TemplatePermissionsChips.methods.permissionLabel.call(
      context,
      'Read template',
    )).to.equal('Read template');
    expect(EditTemplatePermissionForm.methods.permissionLabel.call(
      context,
      'Run template',
    )).to.equal('Run template');
    expect(translations).to.equal(0);
  });

  it('uses CAS deletes and project resource permission for template override actions', () => {
    expect(TemplatePerms.methods.getDeleteItemUrl.call({
      projectId: 7,
      templateId: 9,
      itemId: 11,
      getSingleItemUrl: TemplatePerms.methods.getSingleItemUrl,
    }, { revision: 3 })).to.equal(
      '/api/project/7/templates/9/perms/11?revision=3',
    );
    expect(TemplatePerms.methods.allowActions.call({
      can: (permission) => permission === USER_PERMISSIONS.manageProjectResources,
    })).to.equal(true);
  });

  it('returns to the template list when a read override denies detail access', async () => {
    const denied = new Error('Request failed with status code 403');
    denied.response = { status: 403 };
    let replacement;
    let snackbar;
    const captureSnackbar = (message) => { snackbar = message; };
    EventBus.$once('i-snackbar', captureSnackbar);

    try {
      await TemplateView.methods.loadData.call({
        projectId: 7,
        viewId: 3,
        loadProjectResource: async () => { throw denied; },
        loadProjectResources: async () => [],
        $router: {
          replace: async (destination) => { replacement = destination; },
        },
      });
    } finally {
      EventBus.$off('i-snackbar', captureSnackbar);
    }

    expect(replacement).to.deep.equal({ path: '/project/7/views/3/templates' });
    expect(snackbar).to.deep.equal({
      color: 'error',
      text: 'You do not have permission to view this template.',
    });
  });
});
