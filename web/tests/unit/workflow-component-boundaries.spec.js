import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import DefinitionSettings from '@/components/enhanced/workflow/DefinitionSettings.vue';
import NodeArtifactsEditor from '@/components/enhanced/workflow/NodeArtifactsEditor.vue';
import NodeApprovalPolicy from '@/components/enhanced/workflow/NodeApprovalPolicy.vue';

const textField = {
  name: 'VTextField',
  props: ['modelValue', 'disabled', 'label'],
  template: '<input />',
};
const select = {
  name: 'VSelect',
  props: ['modelValue', 'disabled'],
  template: '<select />',
};
const switchField = {
  name: 'VSwitch',
  props: ['modelValue', 'disabled'],
  template: '<input />',
};
const options = {
  renderStubDefaultSlot: true,
  mocks: { $t: (key) => key },
  stubs: { VTextField: textField, VSelect: select, VSwitch: switchField },
};

describe('workflow component update boundaries', () => {
  it('emits numeric settings before dirty notification without mutating props', () => {
    const events = [];
    const wrapper = shallowMount(DefinitionSettings, {
      global: options,
      props: {
        projectId: 1,
        versionMessage: 'before',
        maxParallelTasks: 1,
        viewRoleIds: [],
        startRoleIds: [],
        parameters: [],
        workflowRoleOptions: [],
        canManage: true,
        canAdminister: false,
      },
      attrs: {
        'onUpdate:maxParallelTasks': (value) => events.push(['value', value]),
        onDirty: () => events.push(['dirty']),
      },
    });
    wrapper.findAllComponents(textField).at(1).vm.$emit('update:modelValue', '4');
    expect(events).to.deep.equal([['value', 4], ['dirty']]);
    expect(wrapper.props('maxParallelTasks')).to.equal(1);
    expect(wrapper.findAllComponents(select).every((field) => field.props('disabled')))
      .to.equal(true);
    wrapper.unmount();
  });

  it('emits artifact updates with numeric-input semantics and explicit switches', () => {
    const outputs = [{
      name: 'result', max_bytes: 1024, sensitive: false, schema: { type: 'string' },
    }];
    const wrapper = shallowMount(NodeArtifactsEditor, {
      global: options,
      props: {
        outputs,
        inputs: [],
        artifactOutputTypes: [],
        reachableArtifactOutputs: [],
        artifactReferenceKey: () => null,
        canManage: true,
      },
    });
    const fields = wrapper.findAllComponents(textField);
    fields.at(1).vm.$emit('update:modelValue', '2048');
    fields.at(1).vm.$emit('update:modelValue', '');
    wrapper.findComponent(switchField).vm.$emit('update:modelValue', true);
    expect(wrapper.emitted('output-field')).to.deep.equal([
      [0, 'max_bytes', 2048], [0, 'max_bytes', ''], [0, 'sensitive', true],
    ]);
    expect(outputs[0].max_bytes).to.equal(1024);
    expect(outputs[0].sensitive).to.equal(false);
    wrapper.unmount();
  });

  it('keeps policy mutation before normalization and preserves disabled controls', () => {
    const events = [];
    const policy = {
      role_ids: [1], mode: 'any_of', minimum_distinct_approvers: 1, initiator_separation: false,
    };
    const wrapper = shallowMount(NodeApprovalPolicy, {
      global: options,
      props: {
        policy,
        workflowRoleOptions: [],
        approvalRoleModeOptions: [],
        approvalTimeoutOutcomeOptions: [],
        canManage: true,
        canAdminister: false,
      },
      attrs: {
        onPolicyField: (...args) => events.push(['field', ...args]),
        onPolicyChange: () => events.push(['normalize']),
      },
    });
    const mode = wrapper.findAllComponents(select).at(1);
    expect(mode.props('disabled')).to.equal(true);
    mode.vm.$emit('update:modelValue', 'all_of');
    expect(events).to.deep.equal([['field', 'mode', 'all_of'], ['normalize']]);
    expect(policy.mode).to.equal('any_of');
    wrapper.unmount();
  });
});
