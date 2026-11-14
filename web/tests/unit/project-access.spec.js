import { expect } from 'chai';
import { USER_PERMISSIONS } from '@/lib/constants';
import {
  hasProjectPermission, permissionLabel, requiredPermissionIds, resourceMatrix, resourcePermission,
  togglePermissionId, RETIRED_PERMISSION_IDS,
} from '@/lib/enhanced/project-access';

describe('project access by permission ID', () => {
  it('builds resource permission IDs and rejects unknown ones', () => {
    expect(resourcePermission('templates', 'create')).to.equal('project.templates.create');
    expect(resourcePermission('access_keys', 'use')).to.equal('project.access_keys.use');
    expect(() => resourcePermission('templates', 'use')).to.throw();
    expect(() => resourcePermission('secrets', 'view')).to.throw();
  });

  it('decides only on the granted ID list and fails closed without one', () => {
    const granted = ['project.read', 'project.templates.view', 'project.templates.update'];
    expect(hasProjectPermission(granted, 'project.templates.update')).to.equal(true);
    expect(hasProjectPermission(granted, 'project.templates.delete')).to.equal(false);
    expect(hasProjectPermission(granted, ['project.read', 'project.templates.view'])).to.equal(true);
    expect(hasProjectPermission(undefined, 'project.read')).to.equal(false);
    expect(hasProjectPermission(granted, 12345)).to.equal(false);
  });

  it('keeps the historic meaning of legacy numeric constants', () => {
    expect(requiredPermissionIds(USER_PERMISSIONS.runProjectTasks)).to.deep.equal(['project.tasks.run']);
    const allMutations = requiredPermissionIds(USER_PERMISSIONS.manageProjectResources);
    expect(allMutations).to.have.length(25);
    expect(allMutations).to.include('project.access_keys.use');
    const templateEditor = ['project.templates.update'];
    const manage = USER_PERMISSIONS.manageProjectResources;
    expect(hasProjectPermission(templateEditor, manage)).to.equal(false);
    expect(hasProjectPermission(allMutations, manage)).to.equal(true);
    expect(requiredPermissionIds(USER_PERMISSIONS.viewProjectResources)).to.have.length(8);
  });

  it('lays the catalog out as resource rows and keeps the rest separate', () => {
    const catalog = [
      { id: 'project.read' },
      { id: 'project.templates.view' }, { id: 'project.templates.create' },
      { id: 'project.templates.update' }, { id: 'project.templates.delete' },
      { id: 'project.access_keys.view' }, { id: 'project.access_keys.use' },
      { id: 'project.resources.manage' },
    ];
    const { rows, other } = resourceMatrix(catalog);
    expect(rows.map((row) => row.resource)).to.deep.equal(['templates', 'access_keys']);
    expect(rows[0].cells.map((cell) => cell.id)).to.deep.equal([
      'project.templates.view', 'project.templates.create', 'project.templates.update', 'project.templates.delete',
    ]);
    expect(rows[1].cells.map((cell) => cell.id)).to.deep.equal(['project.access_keys.view', 'project.access_keys.use']);
    expect(other.map((definition) => definition.id)).to.deep.equal(['project.read']);
    expect(RETIRED_PERMISSION_IDS).to.include('project.resources.manage');
  });

  it('toggles IDs without duplicates', () => {
    expect(togglePermissionId(['a'], 'a', true)).to.deep.equal(['a']);
    expect(togglePermissionId(['a', 'b'], 'a', false)).to.deep.equal(['b']);
    expect(togglePermissionId(undefined, 'c', true)).to.deep.equal(['c']);
  });

  it('names a permission the same way in the matrix, the summary and the effective view', () => {
    const t = (key) => key;
    const catalog = [{ id: 'project.read', description: 'Read project' }];
    expect(permissionLabel('project.templates.update', catalog, t)).to.equal('taskTemplates: permActionUpdate');
    expect(permissionLabel('project.access_keys.use', catalog, t)).to.equal('keyStore: permActionUse');
    expect(permissionLabel('project.read', catalog, t)).to.equal('Read project');
    expect(permissionLabel('workflow.unknown', catalog, t)).to.equal('workflow.unknown');
  });
});
