import { expect } from 'chai';
import axios from 'axios';
import Editor from '@/components/enhanced/SSHKeyBindingsEditor.vue';
import TaskSSHKeys from '@/components/enhanced/TaskSSHKeys.vue';
import TaskForm from '@/components/TaskForm.vue';
import ObjectRefsView from '@/components/ObjectRefsView.vue';
import ItemListPageBase from '@/components/ItemListPageBase';

describe('task SSH key selection', () => {
  function editor(value, inherited = []) {
    const events = [];
    const context = {
      value, inherited, disabled: false, $emit: (name, item) => events.push([name, item]),
    };
    Object.assign(context, Object.fromEntries(Object.entries(Editor.methods)
      .map(([name, method]) => [name, method.bind(context)])));
    return { context, events };
  }

  it('distinguishes inherited selection from an explicit empty override', () => {
    const inherited = [{ access_key_id: 1, hosts: ['github.com'] }];
    const { context, events } = editor(null, inherited);
    context.setInherited(false);
    expect(events[0]).to.deep.equal(['input', inherited]);
    expect(events[0][1][0]).not.to.equal(inherited[0]);
    context.value = events[0][1];
    context.removeBinding(0);
    expect(events[1]).to.deep.equal(['input', []]);
    context.setInherited(true);
    expect(events[2]).to.deep.equal(['input', null]);
  });

  it('changes hosts without mutating parent bindings or copying key material', () => {
    const value = [{ access_key_id: 1, hosts: ['github.com'], private_key: 'not-part-of-dto' }];
    const { context, events } = editor(value);
    context.updateBinding(0, { hosts: [' GitLab.com ', 'github.com', 'gitlab.com'] });
    expect(events[0][1]).to.deep.equal([{ access_key_id: 1, hosts: ['gitlab.com', 'github.com'] }]);
    expect(value[0].hosts).to.deep.equal(['github.com']);
  });

  it('does not emit edits from a disabled editor', () => {
    const { context, events } = editor([]);
    context.disabled = true;
    context.addBinding();
    context.setInherited(true);
    expect(events).to.deep.equal([]);
  });

  it('allows a key without hosts and renders nullable inherited host lists', () => {
    const { context, events } = editor([{ access_key_id: 1, hosts: ['github.com'] }]);
    context.updateBinding(0, { hosts: [] });
    expect(events[0][1]).to.deep.equal([{ access_key_id: 1, hosts: [] }]);
    expect(Editor.methods.hostLabel.call({ $t: (key) => key }, { hosts: null }))
      .to.equal('taskSSHHostsUnmapped');
  });

  it('keeps an empty template override instead of falling back to project defaults', () => {
    const project = { default_ssh_keys: [{ access_key_id: 1, hosts: ['github.com'] }] };
    expect(TaskSSHKeys.computed.inheritedKeys.call({ project, template: { ssh_keys: [] } }))
      .to.deep.equal([]);
    expect(TaskSSHKeys.computed.inheritedKeys.call({ project, template: { ssh_keys: null } }))
      .to.deep.equal(project.default_ssh_keys);
  });

  it('does not offer key overrides for another project’s template', () => {
    expect(TaskForm.computed.sameSSHKeyProject.call({ projectId: 1, template: { project_id: 2 } }))
      .to.equal(false);
    expect(TaskForm.computed.sameSSHKeyProject.call({ projectId: '1', template: { project_id: 1 } }))
      .to.equal(true);
    expect(TaskForm.computed.canOverrideSSHKeys.call({ sameSSHKeyProject: true, can: () => false }))
      .to.equal(false);
    expect(TaskForm.computed.canOverrideSSHKeys.call({ sameSSHKeyProject: true, can: () => true }))
      .to.equal(true);
  });

  it('shows project and task key references in rotation and deletion impact', () => {
    const sections = ObjectRefsView.computed.sections.call({
      objectRefs: { projects: [{ id: 1, name: 'Project' }], tasks: [{ id: 3, name: 'Run' }] },
      $t: (key) => key,
    });
    expect(sections.map((section) => section.slug)).to.deep.equal(['projects', 'tasks']);
  });

  [true, false].forEach((projectBound) => {
    it(`blocks deletion for project bindings only (project bound: ${projectBound})`, async () => {
      const previousAdapter = axios.defaults.adapter;
      try {
        axios.defaults.adapter = async (config) => ({
          data: {
            projects: projectBound ? [{ id: 1 }] : [],
            tasks: [{ id: 3 }],
            templates: [],
            repositories: [],
            inventories: [],
            access_keys: [],
            schedules: [],
          },
          status: 200,
          statusText: 'OK',
          headers: {},
          config,
        });
        const context = {
          itemRefsDialog: false,
          deleteItemDialog: false,
          getSingleItemUrl: () => '/api/project/1/keys/2',
        };
        await ItemListPageBase.methods.askDeleteItem.call(context, 2);
        expect(context.itemRefsDialog).to.equal(projectBound);
        expect(context.deleteItemDialog).to.equal(!projectBound);
      } finally {
        axios.defaults.adapter = previousAdapter;
      }
    });
  });

  it('ignores old project responses after the selected project changes', async () => {
    const previousAdapter = axios.defaults.adapter;
    const pending = [];
    axios.defaults.adapter = (config) => new Promise((resolve) => {
      pending.push((data) => resolve({
        data, status: 200, statusText: 'OK', headers: {}, config,
      }));
    });
    try {
      const context = { ...TaskSSHKeys.data(), projectId: 1 };
      const first = TaskSSHKeys.methods.load.call(context);
      context.projectId = 2;
      const second = TaskSSHKeys.methods.load.call(context);
      pending[2]([{ id: 20, name: 'Current', type: 'ssh' }]);
      pending[3]({ id: 2 });
      await second;
      pending[0]([{ id: 10, name: 'Old', type: 'ssh' }]);
      pending[1]({ id: 1 });
      await first;
      expect(context.project.id).to.equal(2);
      expect(context.keys.map((key) => key.id)).to.deep.equal([20]);
      expect(context.loading).to.equal(false);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });
});
