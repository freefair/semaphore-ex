import { expect } from 'chai';
import TemplateForm from '@/components/TemplateForm.vue';
import TaskDetails from '@/components/TaskDetails.vue';

describe('runner tag placement', () => {
  it('normalizes template tags and preserves the legacy first tag', async () => {
    const context = {
      item: {
        runner_tags: [' GPU ', 'linux', 'gpu', ''],
        runner_tag_match_mode: 'any',
      },
      cronFormat: null,
    };

    await TemplateForm.methods.beforeSave.call(context);

    expect(context.item.runner_tags).to.deep.equal(['gpu', 'linux']);
    expect(context.item.runner_tag).to.equal('gpu');
    expect(context.item.runner_tag_match_mode).to.equal('any');
  });

  it('distinguishes selected placement from an actionable rejection', () => {
    const selected = TaskDetails.computed.placementRejected.call({
      placementDecision: { selected_runner_id: 7 },
    });
    const rejected = TaskDetails.computed.placementRejected.call({
      placementDecision: {
        selected_runner_id: null,
        reason: 'all matching runners are offline',
        action_hint: 'Start a matching runner.',
      },
    });

    expect(selected).to.equal(false);
    expect(rejected).to.equal(true);
  });
});
