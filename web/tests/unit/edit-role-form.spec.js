import './setup';
import { expect } from 'chai';
import EditRoleForm from '@/components/EditRoleForm.vue';

const ctx = { $t: (key) => key };

describe('EditRoleForm', () => {
  describe('name watcher', () => {
    const onNameChange = EditRoleForm.watch['item.name'];

    it('derives the slug of a new role from the name', () => {
      const form = { isNew: true, slugTouched: false, item: { slug: '' } };
      onNameChange.call(form, 'CI Deployer');
      expect(form.item.slug).to.equal('ci-deployer');
      onNameChange.call(form, '  Ops / Prod  ');
      expect(form.item.slug).to.equal('ops-prod');
      onNameChange.call(form, 'Тест');
      expect(form.item.slug).to.equal('');
    });

    it('keeps the slug the user has edited', () => {
      const form = { isNew: true, slugTouched: true, item: { slug: 'mine' } };
      onNameChange.call(form, 'CI Deployer');
      expect(form.item.slug).to.equal('mine');
    });

    it('keeps the slug of an existing role', () => {
      const form = { isNew: false, slugTouched: false, item: { slug: 'legacy' } };
      onNameChange.call(form, 'CI Deployer');
      expect(form.item.slug).to.equal('legacy');
    });
  });

  it('edits legacy project actions without reinterpreting EX workflow bits', () => {
    const form = { item: { permissions: 128 }, permissions: {} };
    EditRoleForm.watch.permissions.handler.call(form, { canRunProjectTasks: true });
    expect(form.item.permissions).to.equal(129);
    EditRoleForm.watch['item.permissions'].handler.call(form, 129);
    expect(form.permissions).to.deep.equal({
      canRunProjectTasks: true,
      canUpdateProject: false,
      canManageProjectResources: false,
      canManageProjectUsers: false,
    });
  });

  it('keeps bits the form does not know', () => {
    const form = { item: { permissions: 1024 | 1 }, permissions: {} };
    EditRoleForm.watch.permissions.handler.call(form, { canRunProjectTasks: false });
    expect(form.item.permissions).to.equal(1024);

    form.permissions = { canManageProjectUsers: true };
    EditRoleForm.methods.beforeSave.call(form);
    expect(form.item.permissions).to.equal(1032);
  });

  it('starts a new role without permissions', () => {
    expect(EditRoleForm.methods.getNewItem.call({ projectId: null })).to.deep.equal({
      name: '', slug: '', permissions: 0, global_permissions: 0,
    });
    expect(EditRoleForm.methods.getNewItem.call({ projectId: 7 })).to.deep.equal({ name: '', permissions: 0 });
  });

  it('derives the slug again after the form is reset', () => {
    const form = { slugTouched: true };
    EditRoleForm.methods.afterReset.call(form);
    expect(form.slugTouched).to.equal(false);
  });

  it('accepts letters of both cases, digits, _ and -', () => {
    expect(EditRoleForm.methods.validateSlug.call(ctx, 'CI_bot-2')).to.equal(true);
    expect(EditRoleForm.methods.validateSlug.call(ctx, 'with space')).to.equal('invalidSlugFormat');
    expect(EditRoleForm.methods.validateSlug.call(ctx, 'a'.repeat(101))).to.equal('invalidSlugFormat');
  });

  it('applies the slug rules only to new roles', () => {
    const legacy = { isNew: false, ...ctx };
    expect(EditRoleForm.computed.slugRules.call(legacy)).to.deep.equal([]);

    const created = {
      isNew: true,
      validateSlug: EditRoleForm.methods.validateSlug,
      ...ctx,
    };
    const rules = EditRoleForm.computed.slugRules.call(created);
    expect(rules).to.have.length(2);
    expect(rules[0]('')).to.equal('slug_required');
    expect(rules[1].call(created, 'my.role')).to.equal('invalidSlugFormat');
  });
});
