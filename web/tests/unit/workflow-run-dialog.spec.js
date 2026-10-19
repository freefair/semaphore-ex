import './setup';
import { expect } from 'chai';
import { mount, createLocalVue } from '@vue/test-utils';
import Vuetify from 'vuetify';
import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';
import SurveyFields from '@/components/SurveyFields.vue';

const workflow = {
  id: 11,
  name: 'Deploy',
  survey_vars: [
    {
      name: 'env', title: 'Environment', type: 'enum', values: [{ name: 'Prod', value: 'prod' }, { name: 'Staging', value: 'staging' }], default_value: 'staging',
    },
    {
      name: 'image_tag', title: 'Image tag', type: '', required: true,
    },
    {
      name: 'replicas', title: 'Replicas', type: 'int', default_value: '2',
    },
    {
      name: 'subnets', title: 'Subnets', type: 'select', values: [{ name: 'a', value: 'a' }], default_value: ['a'],
    },
    {
      name: 'regions', title: 'Regions', type: 'select', values: [{ name: 'eu', value: 'eu' }],
    },
  ],
};

function mountDialog(props = {}) {
  const localVue = createLocalVue();
  return mount(WorkflowRunDialog, {
    localVue,
    vuetify: new Vuetify(),
    propsData: {
      value: true, workflow, projectId: 7, ...props,
    },
    mocks: { $t: (k, params) => (params ? `${k}:${JSON.stringify(params)}` : k) },
    stubs: {
      'v-dialog': { template: '<div><slot /></div>' },
      ExecutionPreflightReview: true,
    },
  });
}

describe('WorkflowRunDialog.vue', () => {
  it('opens with the survey defaults filled in, a select always a list', () => {
    const w = mountDialog();
    expect(w.vm.environment).to.deep.equal({
      env: 'staging', image_tag: '', replicas: '2', subnets: ['a'], regions: [],
    });
    expect(w.findComponent(SurveyFields).props('vars').map((v) => v.name))
      .to.deep.equal(['env', 'image_tag', 'replicas', 'subnets', 'regions']);
    expect(w.text()).to.contain('workflowRunInputs');
  });

  it('does not emit run while a required answer is missing', async () => {
    const w = mountDialog();
    await w.find('[data-testid="workflow-run-dialog-run"]').trigger('click');
    expect(w.emitted('start')).to.equal(undefined);
    expect(w.text()).to.contain('Image tag isRequired');
  });

  it('emits start with survey answers inside the preflight payload', async () => {
    const w = mountDialog();
    w.vm.environment.image_tag = '1.2.3';
    w.vm.environment.env = 'prod';
    w.vm.executionPreflight = { fingerprint: 'fingerprint', review_token: 'token' };
    w.vm.executionPreflightPayloadSignature = JSON.stringify(w.vm.buildPayload());
    await w.vm.$nextTick();
    await w.find('[data-testid="workflow-run-dialog-run"]').trigger('click');
    expect(w.emitted('start')).to.deep.equal([[
      {
        payload: {
          environment: {
            env: 'prod', image_tag: '1.2.3', replicas: '2', subnets: ['a'], regions: [],
          },
        },
        review: { fingerprint: 'fingerprint', reviewToken: 'token' },
      },
    ]]);
  });

  it('reseeds the answers every time it opens', async () => {
    const w = mountDialog({ value: false });
    expect(w.vm.environment).to.deep.equal({});
    await w.setProps({ value: true });
    expect(w.vm.environment.replicas).to.equal('2');
    w.vm.environment.replicas = '9';
    await w.setProps({ value: false });
    await w.setProps({ value: true });
    expect(w.vm.environment.replicas).to.equal('2');
  });
});
