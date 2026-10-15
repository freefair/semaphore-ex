import './setup';
import { expect } from 'chai';
import TemplateView from '@/views/project/TemplateView.vue';
import Templates from '@/views/project/Templates.vue';
import { USER_PERMISSIONS } from '@/lib/constants';

describe('scoped template permissions', () => {
  it('loads template detail without the forbidden repositories collection', async () => {
    const calls = [];
    const context = {
      projectId: 7,
      itemId: 11,
      userPermissions: USER_PERMISSIONS.runProjectTasks,
      isAdmin: false,
      USER_PERMISSIONS,
      loadProjectResource: async () => ({ id: 11, name: 'Scoped template' }),
      loadProjectResources: async (name) => {
        calls.push(name);
        return [{ id: 1, name }];
      },
    };

    await TemplateView.methods.loadData.call(context);

    expect(context.item).to.deep.equal({ id: 11, name: 'Scoped template' });
    expect(context.inventory).to.deep.equal([{ id: 1, name: 'inventory' }]);
    expect(context.environment).to.deep.equal([{ id: 1, name: 'environment' }]);
    expect(context.repositories).to.deep.equal([]);
    expect(calls).to.deep.equal(['inventory', 'environment']);
  });

  it('retains resource collections for a broad project role', async () => {
    const calls = [];
    const context = {
      projectId: 7,
      itemId: 11,
      userPermissions: USER_PERMISSIONS.viewProjectResources,
      isAdmin: false,
      loadProjectResource: async () => ({ id: 11 }),
      loadProjectResources: async (name) => {
        calls.push(name);
        return [{ id: 1, name }];
      },
    };

    await TemplateView.methods.loadData.call(context);

    expect(calls).to.deep.equal(['inventory', 'environment', 'repositories']);
    expect(context.repositories).to.deep.equal([{ id: 1, name: 'repositories' }]);
  });

  it('keeps repository access for an administrator without a project role mask', async () => {
    const calls = [];
    const context = {
      projectId: 7,
      itemId: 11,
      userPermissions: 0,
      isAdmin: true,
      USER_PERMISSIONS,
      loadProjectResource: async () => ({ id: 11 }),
      loadProjectResources: async (name) => {
        calls.push(name);
        return [];
      },
    };
    await TemplateView.methods.loadData.call(context);
    await Templates.methods.loadData.call(context);
    expect(calls).to.deep.equal([
      'inventory', 'environment', 'repositories',
      'inventory', 'environment', 'repositories',
    ]);
  });

  it('keeps permitted resource and view collections while skipping repositories', async () => {
    const calls = [];
    const context = {
      projectId: 7,
      userPermissions: USER_PERMISSIONS.runProjectTasks,
      isAdmin: false,
      USER_PERMISSIONS,
      loadProjectResources: async (name) => {
        calls.push(name);
        return [{ id: 1, name }];
      },
      loadViews: async () => { context.views = [{ id: 1, position: 0 }]; },
      $route: { query: {} },
      views: null,
      viewId: 1,
    };

    await Templates.methods.loadData.call(context);
    await Templates.methods.beforeLoadItems.call(context);

    expect(context.inventory).to.deep.equal([{ id: 1, name: 'inventory' }]);
    expect(context.environment).to.deep.equal([{ id: 1, name: 'environment' }]);
    expect(context.repositories).to.deep.equal([]);
    expect(context.views).to.deep.equal([{ id: 1, position: 0 }]);
    expect(calls).to.deep.equal(['inventory', 'environment']);
  });
});
