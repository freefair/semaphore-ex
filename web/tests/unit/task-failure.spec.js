import { expect } from 'chai';
import { mount } from '@vue/test-utils';
import EnhancedTaskSummary from '@/components/EnhancedTaskSummary.vue';
import { taskFailureLines } from '@/lib/enhanced/task-failure';

describe('task failure reason in the summary', () => {
  it('keeps the last non-empty log lines without terminal colours', () => {
    const output = [
      { output: 'Cloning repository' },
      { output: '' },
      { output: '\u001b[31mfatal: could not read Username\u001b[0m' },
      { output: 'Failed to clone repository' },
    ];
    expect(taskFailureLines(output, 2)).to.deep.equal([
      'fatal: could not read Username', 'Failed to clone repository',
    ]);
    expect(taskFailureLines(undefined)).to.deep.equal([]);
  });

  it('names the failure when a failed task has no host results', async () => {
    const wrapper = mount(EnhancedTaskSummary, {
      props: {
        projectId: 1,
        taskId: 2,
        features: { task_summary: true },
        taskStatus: 'error',
        failureLines: ['Failed to clone repository'],
      },
      global: { mocks: { $t: (key) => key } },
    });
    await wrapper.setData({ loading: false, summary: { state: 'empty' } });
    expect(wrapper.find('[data-testid="task-summary-failed-early"]').exists()).to.equal(true);
    expect(wrapper.find('[data-testid="task-summary-failure-lines"]').text())
      .to.equal('Failed to clone repository');
    expect(wrapper.find('[data-testid="task-summary-empty"]').exists()).to.equal(false);

    await wrapper.setProps({ taskStatus: 'success' });
    expect(wrapper.find('[data-testid="task-summary-failed-early"]').exists()).to.equal(false);
    expect(wrapper.find('[data-testid="task-summary-empty"]').exists()).to.equal(true);
  });
});
