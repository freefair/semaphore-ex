import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import YesNoDialog from '@/components/YesNoDialog.vue';

const button = {
  render(h) {
    return h('button', { on: { click: () => this.$emit('click') } }, this.$slots.default);
  },
};

describe('YesNoDialog.vue', () => {
  let wrapper;

  beforeEach(() => {
    wrapper = shallowMount(YesNoDialog, {
      propsData: { title: 'Confirm action', text: 'Continue with this action?' },
      mocks: { $t: (key) => ({ cancel: 'Cancel', yes: 'Yes' }[key]) },
      stubs: { VBtn: button },
    });
  });

  afterEach(() => wrapper?.destroy());

  it('renders its title, text and translated action labels', () => {
    expect(wrapper.text()).to.include('Confirm action');
    expect(wrapper.text()).to.include('Continue with this action?');
    expect(wrapper.findAll('button').wrappers.map((item) => item.text()))
      .to.deep.equal(['Cancel', 'Yes']);
  });

  [
    { label: 'confirmation', event: 'yes', index: 1 },
    { label: 'cancellation', event: 'no', index: 0 },
  ].forEach(({ label, event, index }) => {
    it(`emits ${label} and closes the bound dialog`, async () => {
      await wrapper.setProps({ value: true });
      expect(wrapper.vm.dialog).to.equal(true);
      await wrapper.findAll('button').at(index).trigger('click');

      expect(wrapper.emitted(event)).to.have.length(1);
      expect(wrapper.emitted('input').slice(-1)).to.deep.equal([[false]]);
      expect(wrapper.vm.dialog).to.equal(false);
    });
  });

  it('supports a custom confirmation label and a hidden cancel button', async () => {
    await wrapper.setProps({ yesButtonTitle: 'Proceed', hideNoButton: true });
    expect(wrapper.findAll('button').wrappers.map((item) => item.text()))
      .to.deep.equal(['Proceed']);
  });
});
