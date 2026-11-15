import { expect } from 'chai';
import { shallowMount, flushPromises } from '@vue/test-utils';

import SecretLocationPicker from '@/components/enhanced/SecretLocationPicker.vue';

// A fake store: Infisical has projects with environments and folders; Vault
// has a mount "secret" with apps/web/db holding two fields.
function infisicalStore(calls) {
  return async (location) => {
    calls.push(location);
    if (!location.project) {
      return [
        { kind: 'project', name: 'Infrastructure', value: 'proj-1' },
        { kind: 'project', name: 'Licenses', value: 'proj-2' },
      ];
    }
    if (!location.environment) {
      return [{ kind: 'environment', name: 'Production', value: 'prod' }];
    }
    if (!location.path) {
      return [
        { kind: 'folder', name: 'apps', value: 'apps' },
        { kind: 'secret', name: 'ROOT_TOKEN', value: 'ROOT_TOKEN' },
      ];
    }
    return [{
      kind: 'secret', name: 'DB_PASSWORD', value: 'DB_PASSWORD', version: 4,
    }];
  };
}

function vaultStore(calls) {
  return async (location) => {
    calls.push(location);
    if (!location.mount) {
      return [{ kind: 'mount', name: 'secret', value: 'secret' }];
    }
    if (location.secret) {
      return [{
        kind: 'field', name: 'password', value: 'password', version: 3,
      }];
    }
    if (!location.path) {
      return [{ kind: 'folder', name: 'apps', value: 'apps' }];
    }
    return [{ kind: 'secret', name: 'db', value: 'apps/db' }];
  };
}

function mountPicker(props) {
  const wrapper = shallowMount(SecretLocationPicker, {
    props: {
      ...props,
      'onUpdate:modelValue': (value) => wrapper.setProps({ modelValue: value }),
    },
    global: { mocks: { $t: (key, values) => (values ? `${key}:${JSON.stringify(values)}` : key) } },
  });
  return wrapper;
}

describe('SecretLocationPicker.vue', () => {
  it('walks an Infisical connection by name and picks a secret', async () => {
    const calls = [];
    const wrapper = mountPicker({ modelValue: {}, storeType: 'infisical', load: infisicalStore(calls) });
    await flushPromises();
    expect(wrapper.vm.projects.map((p) => p.name)).to.deep.equal(['Infrastructure', 'Licenses']);

    await wrapper.vm.selectProject('proj-1');
    await flushPromises();
    expect(wrapper.vm.environments.map((e) => e.name)).to.deep.equal(['Production']);

    await wrapper.vm.selectEnvironment('prod');
    await flushPromises();
    expect(wrapper.vm.crumbs.map((c) => c.name)).to.deep.equal(['Production']);

    await wrapper.vm.choose({ kind: 'folder', name: 'apps', value: 'apps' });
    await flushPromises();
    expect(wrapper.vm.crumbs.map((c) => c.name)).to.deep.equal(['Production', 'apps']);

    await wrapper.vm.choose({
      kind: 'secret', name: 'DB_PASSWORD', value: 'DB_PASSWORD', version: 4,
    });
    expect(wrapper.props('modelValue')).to.include({
      project: 'proj-1', environment: 'prod', path: 'apps', secret: 'DB_PASSWORD',
    });
    expect(wrapper.vm.summary).to.equal('Infrastructure / Production / apps / DB_PASSWORD');
    expect(calls).to.deep.include({ project: 'proj-1', environment: 'prod', path: 'apps' });
  });

  it('picks a Vault field and pins its current version', async () => {
    const calls = [];
    const wrapper = mountPicker({ modelValue: {}, storeType: 'vault', load: vaultStore(calls) });
    await flushPromises();
    await wrapper.vm.selectMount('secret');
    await flushPromises();
    await wrapper.vm.choose({ kind: 'folder', name: 'apps', value: 'apps' });
    await flushPromises();
    await wrapper.vm.choose({ kind: 'secret', name: 'db', value: 'apps/db' });
    await flushPromises();

    expect(wrapper.props('modelValue')).to.include({
      mount: 'secret', secret: 'apps/db', field: 'password', version: 3,
    });
    expect(wrapper.vm.summary).to.equal('secret / apps/db / password');
  });

  it('offers folders only and selects the open folder as a scope', async () => {
    const wrapper = mountPicker({
      modelValue: { project: 'proj-1', environment: 'prod' }, storeType: 'infisical', load: infisicalStore([]), target: 'folder',
    });
    await flushPromises();
    expect(wrapper.vm.visibleEntries.map((e) => e.kind)).to.deep.equal(['folder']);
    await wrapper.vm.choose({ kind: 'folder', name: 'apps', value: 'apps' });
    await flushPromises();
    expect(wrapper.props('modelValue').path).to.equal('apps');
    expect(wrapper.emitted('summary').at(-1)).to.deep.equal(['Infrastructure / Production / apps']);
  });

  it('filters the open folder by a search term', async () => {
    const wrapper = mountPicker({
      modelValue: { project: 'proj-1', environment: 'prod' }, storeType: 'infisical', load: infisicalStore([]),
    });
    await flushPromises();
    wrapper.vm.filter = 'root';
    expect(wrapper.vm.visibleEntries.map((e) => e.name)).to.deep.equal(['ROOT_TOKEN']);
  });

  it('reopens a saved reference where it points', async () => {
    const calls = [];
    const wrapper = mountPicker({
      modelValue: {
        mount: 'secret', secret: 'apps/db', field: 'password', version: 3,
      },
      storeType: 'vault',
      load: vaultStore(calls),
    });
    await flushPromises();
    expect(wrapper.vm.folder).to.equal('apps');
    expect(calls).to.deep.include({ mount: 'secret', secret: 'apps/db' });
    expect(wrapper.vm.fields.map((f) => f.name)).to.deep.equal(['password']);
  });

  it('shows the server message and offers manual entry when listing fails', async () => {
    const failing = async () => {
      const error = new Error('denied');
      error.response = { data: { error: 'The connection may not list this.', reason: 'permission' } };
      throw error;
    };
    const wrapper = mountPicker({ modelValue: {}, storeType: 'vault', load: failing });
    await flushPromises();
    expect(wrapper.vm.error).to.deep.equal({ message: 'The connection may not list this.', reason: 'permission' });
    expect(wrapper.find('[data-testid="secret-picker-error"]').exists()).to.equal(true);
  });
});
