import { expect } from 'chai';
import { computed, h } from 'vue';
import { shallowMount } from '@vue/test-utils';
import PermissionsCheck from '@/components/PermissionsCheck';
import { USER_PERMISSIONS } from '@/lib/constants';

const Host = {
  mixins: [PermissionsCheck],
  props: { item: Object },
  render: () => h('div'),
};

function mountHost(props, access) {
  return shallowMount(Host, {
    props,
    global: { provide: { projectAccess: computed(() => access) } },
  });
}

function can(props, access, permission) {
  return mountHost(props, access).vm.can(permission);
}

describe('PermissionsCheck mixin', () => {
  const {
    runProjectTasks, updateProject, manageProjectResources, manageProjectUsers,
  } = USER_PERMISSIONS;
  const RUN = 'project.tasks.run';
  const UPDATE = 'project.settings.update';
  const MEMBERS = 'project.members.manage';

  it('grants everything to admins regardless of permissions', () => {
    expect(can({ isAdmin: true }, { ids: [] }, manageProjectUsers)).to.equal(true);
    expect(can({}, { admin: true, ids: [] }, manageProjectUsers)).to.equal(true);
  });

  const tests = [
    {
      name: 'no permissions', ids: [], permission: runProjectTasks, expected: false,
    },
    {
      name: 'exact permission', ids: [RUN], permission: runProjectTasks, expected: true,
    },
    {
      name: 'other permission only', ids: [UPDATE], permission: runProjectTasks, expected: false,
    },
    {
      name: 'permission ID', ids: [RUN, 'project.templates.update'], permission: 'project.templates.update', expected: true,
    },
    {
      name: 'missing permission ID', ids: [RUN], permission: 'project.templates.update', expected: false,
    },
    {
      name: 'list of IDs fully present', ids: [RUN, UPDATE, MEMBERS], permission: [RUN, UPDATE], expected: true,
    },
    {
      name: 'list of IDs partially present', ids: [RUN], permission: [RUN, UPDATE], expected: false,
    },
    {
      name: 'one resource is not the historic manage boundary', ids: ['project.templates.update'], permission: manageProjectResources, expected: false,
    },
    {
      name: 'no list loaded', ids: undefined, permission: runProjectTasks, expected: false,
    },
  ];

  tests.forEach(({
    name, ids, permission, expected,
  }) => {
    it(`project permissions: ${name}`, () => {
      expect(can({}, { admin: false, ids }, permission)).to.equal(expected);
    });
  });

  it('ignores the numeric mask prop for project-level decisions', () => {
    // A mask above 2^53 arrives rounded in the browser; only the ID list counts.
    expect(can({ userPermissions: 2 ** 55 }, { ids: [] }, manageProjectUsers)).to.equal(false);
  });

  it('denies when the item has no permissions at all', () => {
    expect(can({ item: { permissions: 0 } }, { ids: [RUN] }, runProjectTasks)).to.equal(false);
  });

  it('checks the item it is given instead of its own', () => {
    const { vm } = mountHost({}, { ids: [] });
    expect(vm.can(runProjectTasks, { permissions: runProjectTasks })).to.equal(true);
    expect(vm.can(runProjectTasks, { permissions: 0 })).to.equal(false);
  });

  it('prefers the item mask over the project list when the item has one', () => {
    const props = { item: { permissions: runProjectTasks } };
    const access = { ids: [RUN, UPDATE, MEMBERS] };
    expect(can(props, access, runProjectTasks)).to.equal(true);
    expect(can(props, access, manageProjectUsers)).to.equal(false);
  });

  it('falls back to the project list when the item has no permissions field', () => {
    expect(can({ item: { id: 1 } }, { ids: [MEMBERS] }, manageProjectUsers)).to.equal(true);
  });

  it('decides ID requests on the project list even when the item has a mask', () => {
    const props = { item: { permissions: updateProject } };
    expect(can(props, { ids: ['project.views.update'] }, 'project.views.update')).to.equal(true);
  });
});
