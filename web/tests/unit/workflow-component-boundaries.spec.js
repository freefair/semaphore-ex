import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import DefinitionSettings from '@/components/enhanced/workflow/DefinitionSettings.vue';
import NodeArtifactsEditor from '@/components/enhanced/workflow/NodeArtifactsEditor.vue';
import NodeApprovalPolicy from '@/components/enhanced/workflow/NodeApprovalPolicy.vue';

const textField = {
  name: 'VTextField',
  props: ['value', 'disabled', 'label'],
  render(h) { return h('input'); },
};
const select = {
  name: 'VSelect',
  props: ['value', 'disabled'],
  render(h) { return h('select'); },
};
const switchField = {
  name: 'VSwitch',
  model: { prop: 'inputValue', event: 'change' },
  props: ['inputValue', 'disabled'],
  render(h) { return h('input'); },
};
const options = {
  mocks: { $t: (key) => key },
  stubs: { VTextField: textField, VSelect: select, VSwitch: switchField },
};

describe('workflow component update boundaries', () => {
  it('emits numeric settings before dirty notification without mutating props', () => {
    const events = [];
    const wrapper = shallowMount(DefinitionSettings, {
      ...options,
      propsData: {
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
      listeners: {
        'update:maxParallelTasks': (value) => events.push(['value', value]),
        dirty: () => events.push(['dirty']),
      },
    });
    wrapper.findAllComponents(textField).at(1).vm.$emit('input', '4');
    expect(events).to.deep.equal([['value', 4], ['dirty']]);
    expect(wrapper.props('maxParallelTasks')).to.equal(1);
    expect(wrapper.findAllComponents(select).wrappers.every((field) => field.props('disabled')))
      .to.equal(true);
    wrapper.destroy();
  });

  it('emits artifact updates with numeric-input semantics and explicit switches', () => {
    const outputs = [{
      name: 'result', max_bytes: 1024, sensitive: false, schema: { type: 'string' },
    }];
    const wrapper = shallowMount(NodeArtifactsEditor, {
      ...options,
      propsData: {
        outputs,
        inputs: [],
        artifactOutputTypes: [],
        reachableArtifactOutputs: [],
        artifactReferenceKey: () => null,
        canManage: true,
      },
    });
    const fields = wrapper.findAllComponents(textField);
    fields.at(1).vm.$emit('input', '2048');
    fields.at(1).vm.$emit('input', '');
    wrapper.findComponent(switchField).vm.$emit('change', true);
    expect(wrapper.emitted('output-field')).to.deep.equal([
      [0, 'max_bytes', 2048], [0, 'max_bytes', ''], [0, 'sensitive', true],
    ]);
    expect(outputs[0].max_bytes).to.equal(1024);
    expect(outputs[0].sensitive).to.equal(false);
    wrapper.destroy();
  });

  it('keeps policy mutation before normalization and preserves disabled controls', () => {
    const events = [];
    const policy = {
      role_ids: [1], mode: 'any_of', minimum_distinct_approvers: 1, initiator_separation: false,
    };
    const wrapper = shallowMount(NodeApprovalPolicy, {
      ...options,
      propsData: {
        policy,
        workflowRoleOptions: [],
        approvalRoleModeOptions: [],
        approvalTimeoutOutcomeOptions: [],
        canManage: true,
        canAdminister: false,
      },
      listeners: {
        'policy-field': (...args) => events.push(['field', ...args]),
        'policy-change': () => events.push(['normalize']),
      },
    });
    const mode = wrapper.findAllComponents(select).at(1);
    expect(mode.props('disabled')).to.equal(true);
    mode.vm.$emit('input', 'all_of');
    mode.vm.$emit('change', 'all_of');
    expect(events).to.deep.equal([['field', 'mode', 'all_of'], ['normalize']]);
    expect(policy.mode).to.equal('any_of');
    wrapper.destroy();
  });
});
