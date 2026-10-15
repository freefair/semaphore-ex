import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import RepositoryForm from '@/components/RepositoryForm.vue';

describe('local repository shared checkout', () => {
  let adapter;
  let wrapper;
  beforeEach(() => {
    adapter = axios.defaults.adapter;
    axios.defaults.adapter = async () => ({ data: [] });
  });
  afterEach(() => {
    if (wrapper) wrapper.destroy();
    axios.defaults.adapter = adapter;
  });

  ['/srv/repository', 'C:\\repository', '\\\\server\\share'].forEach((url) => {
    it(`clears shared checkout when entering ${url} and when loading a stale record`, async () => {
      wrapper = shallowMount(RepositoryForm, {
        propsData: { itemId: 'new', projectId: 1 },
        mocks: { $t: (key) => key },
      });
      await wrapper.vm.$nextTick();
      await wrapper.setData({ item: { git_url: 'https://example.com/repo', monorepo: true } });
      await wrapper.setData({ item: { git_url: url, monorepo: true } });
      expect(wrapper.vm.item.monorepo).to.equal(false);
      wrapper.vm.item.monorepo = true;
      await wrapper.vm.beforeSave();
      expect(wrapper.vm.item.monorepo).to.equal(false);
    });
  });

  it('clears shared checkout through the local chip and preserves Git clones', async () => {
    wrapper = shallowMount(RepositoryForm, {
      propsData: { itemId: 'new', projectId: 1 },
      mocks: { $t: (key) => key },
    });
    await wrapper.vm.$nextTick();
    await wrapper.setData({ item: { git_url: 'file:///srv/repo', monorepo: true } });
    await wrapper.vm.beforeSave();
    expect(wrapper.vm.item.monorepo).to.equal(true);
    wrapper.vm.setType('local');
    await wrapper.vm.$nextTick();
    expect(wrapper.vm.item.monorepo).to.equal(false);
  });

  it('preserves a shared Git checkout using a one-letter SSH host alias', async () => {
    wrapper = shallowMount(RepositoryForm, {
      propsData: { itemId: 'new', projectId: 1 },
      mocks: { $t: (key) => key },
    });
    await wrapper.vm.$nextTick();
    await wrapper.setData({ item: { git_url: 'c:org/repo.git', monorepo: true } });
    await wrapper.vm.beforeSave();
    expect(wrapper.vm.type).to.equal('ssh');
    expect(wrapper.vm.item.monorepo).to.equal(true);
  });
});
