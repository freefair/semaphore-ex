import { expect } from 'chai';
import axios from 'axios';
import Editor from '@/components/enhanced/TaskGroupsEditor.vue';
import Membership from '@/components/enhanced/TaskGroupMembership.vue';
import TaskGroups from '@/views/project/TaskGroups.vue';
import TaskGroupForm from '@/components/enhanced/TaskGroupForm.vue';
import { ENHANCED_USER_PERMISSIONS as permissions } from '@/lib/enhanced/permissions';

describe('managed task groups', () => {
  it('uses owning-project permissions separately for editing, deletion and sharing', () => {
    const context = {
      projectId: 2,
      isAdmin: false,
      can: () => true,
      ownerPermissions: { 1: permissions.updateTaskGroups },
    };
    const can = (owner, permission) => TaskGroups.methods.canInProject
      .call(context, owner, permission);
    expect(can(1, permissions.updateTaskGroups)).to.equal(true);
    expect(can(1, permissions.deleteTaskGroups)).to.equal(false);
    expect(can(1, permissions.shareTaskGroups)).to.equal(false);
    expect(can(3, permissions.updateTaskGroups)).to.equal(false);
    expect(can(2, permissions.updateTaskGroups)).to.equal(true);
  });

  it('does not reuse consumer permissions when owner access is absent or fails', async () => {
    const originalGet = axios.get;
    const requests = [];
    axios.get = async (url) => {
      requests.push(url);
      if (url === '/api/projects') return { data: [{ id: 2 }, { id: 1 }, { id: 3 }] };
      if (url === '/api/project/1/role') return { data: { permissions: permissions.updateTaskGroups } };
      throw new Error('role unavailable');
    };
    const context = {
      projectId: 2,
      isAdmin: false,
      items: [{ project_id: 1 }, { project_id: 1 }, { project_id: 3 }, { project_id: 4 }],
      ownerPermissions: {},
    };
    try {
      await TaskGroups.methods.loadOwnerPermissions.call(context);
      expect(context.ownerPermissions).to.deep.equal({ 1: permissions.updateTaskGroups });
      expect(requests).to.deep.equal(['/api/projects', '/api/project/1/role', '/api/project/3/role']);
    } finally {
      axios.get = originalGet;
    }
  });

  it('edits and deletes shared groups through the owning project endpoint', async () => {
    const context = {
      projectId: 2,
      itemId: 7,
      items: [{ id: 7, project_id: 1, revision: 3 }],
      loadItems: async () => {},
    };
    context.itemProjectId = TaskGroups.computed.itemProjectId.call(context);
    expect(context.itemProjectId).to.equal(1);
    const form = { projectId: context.itemProjectId, itemId: 7 };
    form.getItemsUrl = () => TaskGroupForm.methods.getItemsUrl.call(form);
    expect(TaskGroupForm.methods.getSingleItemUrl.call(form)).to.equal('/api/project/1/task_groups/7');
    const originalDelete = axios.delete;
    let request;
    axios.delete = async (...args) => { request = args; };
    try {
      await TaskGroups.methods.deleteItem.call(context, 7);
      expect(request).to.deep.equal(['/api/project/1/task_groups/7', { data: { revision: 3 } }]);
    } finally {
      axios.delete = originalDelete;
    }
    context.itemId = 'new';
    expect(TaskGroups.computed.itemProjectId.call(context)).to.equal(2);
  });

  it('rejects the intersection of conflicting runner definitions', () => {
    expect(Editor.computed.conflict.call({
      value: [1, 2],
      groups: [{ id: 1, runner_ids: [4, 5] }, { id: 2, runner_ids: [6] }],
    })).to.equal(true);
  });

  it('accepts every selected group only when they have a common runner', () => {
    expect(Editor.computed.conflict.call({
      value: [1, 2, 3],
      groups: [{ id: 1, runner_ids: [4, 5] }, { id: 2, runner_ids: [5, 6] },
        { id: 3, runner_ids: [] }],
    })).to.equal(false);
  });

  it('distinguishes shared catalog records by ID instead of allowing free-text names', () => {
    const options = Editor.computed.options.call({
      projectId: 1,
      groups: [{ id: 8, project_id: 1, name: 'Deploy' },
        { id: 9, project_id: 2, name: 'Deploy' }],
      $t: () => 'Shared',
    });
    expect(options.map((entry) => entry.id)).to.deep.equal([8, 9]);
    expect(options.map((entry) => entry.label)).to.deep.equal(['Deploy', 'Deploy · Shared']);
  });

  it('resolves historical group IDs to accessible catalog names', () => {
    const groups = Membership.computed.groups.call({
      value: ['group/42'],
      catalog: [{ id: 42, name: 'Infrastructure' }],
      $t: () => 'missing',
    });
    expect(groups).to.deep.equal([{ key: 'group/42', label: 'Infrastructure' }]);
  });
});
