import { expect } from 'chai';
import { shallowMount } from '@vue/test-utils';
import axios from 'axios';
import TaskForm from '@/components/TaskForm.vue';
import TemplateForm from '@/components/TemplateForm.vue';

const settle = () => new Promise((resolve) => { setTimeout(resolve, 350); });

describe('monorepo task and template forms', () => {
  let adapter;
  let post;
  let wrapper;

  beforeEach(() => { adapter = axios.defaults.adapter; post = axios.post; });
  afterEach(() => {
    if (wrapper) wrapper.destroy();
    wrapper = null;
    axios.defaults.adapter = adapter;
    axios.post = post;
  });

  [false, true].forEach((monorepo) => {
    it(`uses authorized template policy without repository reads (monorepo=${monorepo})`, async () => {
      const repositoryCalls = [];
      axios.defaults.adapter = async (config) => {
        repositoryCalls.push(config.url);
        throw new Error('Repository metadata is forbidden for this run-only role');
      };
      axios.post = async () => ({ data: { fingerprint: 'review', review_token: 'review-token', findings: [] } });
      wrapper = shallowMount(TaskForm, {
        propsData: {
          itemId: 'new',
          projectId: 7,
          template: {
            id: 11,
            project_id: 7,
            repository_id: 3,
            app: 'bash',
            type: '',
            survey_vars: [],
            allow_override_branch_in_task: true,
            repository_monorepo: monorepo,
          },
        },
        mocks: { $t: (key) => key },
      });
      await settle();
      expect(repositoryCalls).to.deep.equal([]);
      expect(wrapper.vm.formError).to.equal(null);
      expect(wrapper.vm.executionReady).to.equal(true);
      expect(wrapper.vm.canOverrideBranch).to.equal(!monorepo);
    });
  });

  it('hides the branch prompt when a stale template flag meets monorepo policy', () => {
    const context = {
      needField: () => true,
      template: { allow_override_branch_in_task: true, repository_monorepo: true },
    };
    expect(TaskForm.computed.canOverrideBranch.call(context)).to.equal(false);
    context.template.repository_monorepo = false;
    expect(TaskForm.computed.canOverrideBranch.call(context)).to.equal(true);
    context.template.allow_override_branch_in_task = false;
    expect(TaskForm.computed.canOverrideBranch.call(context)).to.equal(false);
  });

  it('clears stale branch settings when saving an already selected monorepo', async () => {
    const context = {
      isMonorepo: true,
      item: { git_branch: 'old-release', allow_override_branch_in_task: true },
      cronFormat: null,
    };
    await TemplateForm.methods.beforeSave.call(context);
    expect(context.item.git_branch).to.equal(null);
    expect(context.item.allow_override_branch_in_task).to.equal(false);
  });

  it('preserves ordinary repository branch settings on save', async () => {
    const context = {
      isMonorepo: false,
      item: { git_branch: 'release', allow_override_branch_in_task: true },
      cronFormat: null,
    };
    await TemplateForm.methods.beforeSave.call(context);
    expect(context.item.git_branch).to.equal('release');
    expect(context.item.allow_override_branch_in_task).to.equal(true);
  });
});
