import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import ExecutionPreflightReview from '@/components/ExecutionPreflightReview.vue';
import NewTaskDialog from '@/components/NewTaskDialog.vue';
import axios from 'axios';
import TemplateForm from '@/components/TemplateForm.vue';
import en from '@/lang/enhanced/en';
import de from '@/lang/enhanced/de';
import Vuetify from 'vuetify';

describe('task dialog usability', () => {
  it('omits only routine provisional findings and retains actionable findings', () => {
    const findings = [
      { code: 'provisional_placement', severity: 'warning', message: 'Routine placement' },
      { code: 'runner_busy', severity: 'warning', message: 'Runner busy' },
      { code: 'provisional_placement', severity: 'denial', message: 'Placement denied' },
      { code: 'policy_denied', severity: 'denial', message: 'Policy denied' },
    ];
    const wrapper = shallowMount(ExecutionPreflightReview, {
      propsData: { plan: { definition: { name: 'Test' }, findings } },
      mocks: { $t: (key) => key },
    });
    expect(wrapper.text()).not.to.contain('Routine placement');
    expect(wrapper.text()).to.contain('Runner busy');
    expect(wrapper.text()).to.contain('Placement denied');
    expect(wrapper.text()).to.contain('Policy denied');
    wrapper.destroy();
  });

  it('shows provisional state only for a provisional placement and preserves bindings', () => {
    const wrapper = shallowMount(ExecutionPreflightReview, {
      propsData: {
        plan: {
          definition: { name: 'Test' },
          placements: [
            { node_id: 1, selected_runner_name: 'Confirmed', provisional: false },
            { node_id: 2, selected_runner_name: 'Candidate', provisional: true },
          ],
          references: [
            {
              kind: 'credential', id: 9, name: 'SSH key', binding_target: 'repository.ssh',
            },
            {
              kind: 'credential', id: 9, name: 'SSH key', binding_target: 'inventory.repository.ssh',
            },
          ],
        },
      },
      mocks: { $t: (key) => key },
    });
    expect(wrapper.findAll('[data-testid="placement-provisional"]')).to.have.length(1);
    expect(wrapper.text()).to.contain('repository.ssh');
    expect(wrapper.text()).to.contain('inventory.repository.ssh');
    expect(wrapper.text()).not.to.contain('executionPreflightNoSecretValues');
    wrapper.destroy();
  });

  it('keeps the full template name in the task heading', () => {
    const name = 'Infrastructure production verification';
    expect(NewTaskDialog.computed.templateTitle.call({ template: { name } })).to.equal(name);
  });

  [en, de].forEach((messages) => {
    it('distinguishes independently editable Terraform defaults and overrides', async () => {
      const adapter = axios.defaults.adapter;
      axios.defaults.adapter = async (config) => ({
        data: config.url === '/api/info' ? {} : [],
        status: 200,
        config,
      });
      let wrapper;
      try {
        wrapper = shallowMount(TemplateForm, {
          vuetify: new Vuetify(),
          propsData: {
            itemId: 'new', projectId: 7, app: 'terraform', features: {},
          },
          mocks: { $t: (key) => messages[key] || key },
        });
        await new Promise((resolve) => { setTimeout(resolve, 20); });
        const checkboxes = wrapper.findAll('v-checkbox-stub').wrappers;
        const defaults = checkboxes.find((box) => box.attributes('label') === messages.templateAutoApproveDefault);
        const overrides = checkboxes.find((box) => box.attributes('label') === messages.templateAutoApproveOverride);
        expect(defaults).not.to.equal(undefined);
        expect(overrides).not.to.equal(undefined);
        defaults.vm.$emit('change', true);
        await wrapper.vm.$nextTick();
        expect(wrapper.vm.item.task_params.auto_approve).to.equal(true);
        expect(wrapper.vm.item.task_params.allow_auto_approve).not.to.equal(true);
        overrides.vm.$emit('change', true);
        defaults.vm.$emit('change', false);
        await wrapper.vm.$nextTick();
        expect(wrapper.vm.item.task_params.auto_approve).to.equal(false);
        expect(wrapper.vm.item.task_params.allow_auto_approve).to.equal(true);
      } finally {
        if (wrapper) wrapper.destroy();
        axios.defaults.adapter = adapter;
      }
    });
  });
});
