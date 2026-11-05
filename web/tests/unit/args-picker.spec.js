import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';

import ArgsPicker from '@/components/ArgsPicker.vue';

const textField = {
  props: ['modelValue'],
  emits: ['update:modelValue', 'keydown'],
  template: `<input :value="modelValue" @input="$emit('update:modelValue', $event.target.value)"
    @keydown="$emit('keydown', $event)" />`,
};

const form = {
  data: () => ({ valid: true }),
  methods: {
    async validate() { return { valid: this.valid }; },
    resetValidation() { this.valid = true; },
  },
  template: '<form><slot /></form>',
};

describe('ArgsPicker.vue', () => {
  let wrapper;

  function mountArgs(vars = []) {
    wrapper = shallowMount(ArgsPicker, {

      props: { vars },
      global: {
        renderStubDefaultSlot: true,
        mocks: { $t: (key) => key },
        stubs: {
          VDialog: { template: '<div><slot /></div>' },
          VTextField: textField,
          VForm: form,
        },
      },
    });
    return wrapper;
  }

  afterEach(() => {
    wrapper?.unmount();
    wrapper = null;
  });

  it('adds the trimmed input on Enter and closes the edit dialog', async () => {
    const vars = [];
    mountArgs(vars);
    wrapper.vm.editVar(null);
    await wrapper.vm.$nextTick();
    await wrapper.find('input').setValue('  --check  ');
    await wrapper.find('input').trigger('keydown.enter');

    expect(wrapper.emitted('change')).to.deep.equal([[['--check']]]);
    expect(wrapper.vm.editDialog).to.equal(false);
    expect(vars).to.deep.equal([]);
  });

  it('keeps invalid input open without emitting a change', async () => {
    mountArgs();
    wrapper.vm.editVar(null);
    await wrapper.vm.$nextTick();
    await wrapper.findComponent(form).setData({ valid: false });
    await wrapper.find('input').trigger('keydown.enter');

    expect(wrapper.emitted('change')).to.equal(undefined);
    expect(wrapper.vm.editDialog).to.equal(true);
  });

  it('replaces the selected argument on form submission', async () => {
    mountArgs(['--diff', '--verbose']);
    wrapper.vm.editVar(0);
    await wrapper.vm.$nextTick();
    await wrapper.find('input').setValue('--check');
    await wrapper.find('form').trigger('submit');

    expect(wrapper.emitted('change')).to.deep.equal([[['--check', '--verbose']]]);
    expect(wrapper.vm.editDialog).to.equal(false);
  });
});
