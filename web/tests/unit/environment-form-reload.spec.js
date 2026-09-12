import { expect } from 'chai';
import { mount } from '@vue/test-utils';
import EnvironmentForm from '@/components/EnvironmentForm.vue';
import mockAxios from './helpers/axiosMock';

const FormStub = {
  render: (h) => h('form'),
  methods: { validate: () => true, resetValidation: () => {} },
};
const Host = {
  extends: EnvironmentForm,
  components: { FormStub },
  render(h) { return h('form-stub', { ref: 'form' }); },
};
const flush = async () => new Promise((resolve) => { setTimeout(resolve, 0); });

describe('variable group editor reload', () => {
  let http;
  let wrapper;
  const groups = {
    1: {
      id: 1, name: 'Group A', json: '{"group":"A"}', env: '{}',
    },
    2: {
      id: 2, name: 'Group B', json: '{"group":"B","enabled":false,"count":0}', env: '{}',
    },
  };
  beforeEach(() => {
    http = mockAxios();
    http.respond((req) => {
      if (['put', 'post'].includes(req.method)) return req.data;
      if (req.url.endsWith('/secret_storages')) return [];
      const id = req.url.split('/').pop();
      return { ...groups[id] };
    });
  });
  afterEach(() => { wrapper?.destroy(); http.restore(); });

  [
    { name: 'another group after valid YAML editing', yaml: 'group: unsaved-A', id: 2 },
    { name: 'another group after invalid YAML editing', yaml: 'group: [invalid', id: 2 },
    { name: 'the same group after canceling YAML edits', yaml: 'group: unsaved-A', id: 1 },
    { name: 'a new group after YAML editing', yaml: 'group: unsaved-A', id: 'new' },
  ].forEach(({ name, yaml, id }) => {
    it(`loads only persisted values for ${name}`, async () => {
      wrapper = mount(Host, { propsData: { projectId: 7, itemId: 1 } });
      await flush();
      await wrapper.setData({ extraVarsEditMode: 'yaml' });
      await wrapper.setData({ yaml });
      await wrapper.setProps({ itemId: id });
      await wrapper.vm.reset();
      await flush();

      expect(wrapper.vm.formError).to.equal(null);
      expect(wrapper.vm.extraVarsEditMode).to.equal('table');
      const saved = await wrapper.vm.save();
      const expected = id === 'new' ? {} : JSON.parse(groups[id].json);
      expect(JSON.parse(saved.json)).to.deep.equal(expected);
      expect(JSON.parse(http.requests[http.requests.length - 1].data.json)).to.deep.equal(expected);

      wrapper.vm.extraVars.push({ name: 'fresh', type: 'string', value: 'edit' });
      await wrapper.setData({ extraVarsEditMode: 'yaml' });
      wrapper.vm.beforeSave();
      expect(JSON.parse(wrapper.vm.item.json)).to.deep.equal({ ...expected, fresh: 'edit' });
    });
  });
});
