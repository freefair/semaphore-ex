import { expect } from 'chai';
import axios from 'axios';
import Runners from '@/views/Runners.vue';

function lifecycleContext(overrides = {}) {
  return {
    deletingRunnerIds: [],
    cacheCleaningRunnerIds: [],
    isRunnerDeleting: Runners.methods.isRunnerDeleting,
    isRunnerCacheCleaning: Runners.methods.isRunnerCacheCleaning,
    runnerLifecycleState: Runners.methods.runnerLifecycleState,
    ...overrides,
  };
}

describe('project runner lifecycle', () => {
  it('derives every observable lifecycle state with deterministic priority', () => {
    const runner = {
      id: 7,
      registered: true,
      active: true,
      touched: '2026-08-26T10:00:00Z',
      cleaning_requested: null,
    };
    const context = lifecycleContext();

    expect(Runners.methods.runnerLifecycleState.call(context, runner)).to.equal('registered');
    expect(Runners.methods.runnerLifecycleState.call(context, { ...runner, active: false }))
      .to.equal('inactive');
    expect(Runners.methods.runnerLifecycleState.call(context, { ...runner, registered: false }))
      .to.equal('pending');
    expect(Runners.methods.runnerLifecycleState.call(context, {
      ...runner,
      cleaning_requested: '2026-08-26T10:01:00Z',
    })).to.equal('cache-cleaning');
    expect(Runners.methods.runnerLifecycleState.call(context, {
      ...runner,
      cleaning_requested: runner.touched,
    })).to.equal('cache-cleaning');

    context.deletingRunnerIds.push(runner.id);
    expect(Runners.methods.runnerLifecycleState.call(context, runner)).to.equal('deleting');
  });

  it('turns assignment conflicts into actionable task details', () => {
    const context = {
      $t: (_key, values) => `blocked: ${values.assignments}`,
    };
    const message = Runners.methods.lifecycleErrorMessage.call(context, {
      response: {
        data: {
          error: 'PROJECT_RUNNER_ASSIGNMENTS_ACTIVE',
          assignments: [
            { task_id: 41, status: 'running' },
            { task_id: 42, status: 'waiting' },
          ],
        },
      },
    });

    expect(message).to.equal('blocked: #41 (running), #42 (waiting)');
  });

  it('exposes deleting while the request is pending and clears it afterwards', async () => {
    const previousAdapter = axios.defaults.adapter;
    let completeRequest;
    axios.defaults.adapter = (config) => new Promise((resolve) => {
      completeRequest = () => resolve({
        data: null,
        status: 204,
        statusText: 'No Content',
        headers: {},
        config,
      });
    });

    try {
      const item = { id: 9, project_id: 3 };
      const context = {
        itemId: null,
        items: [item],
        deletingRunnerIds: [],
        getSingleItemUrl: () => '/api/project/3/runners/9',
        getEventName: () => 'runner-changed',
        loadItems: async () => [],
        lifecycleErrorMessage: Runners.methods.lifecycleErrorMessage,
      };

      const deletion = Runners.methods.deleteItem.call(context, item.id);
      expect(context.deletingRunnerIds).to.deep.equal([item.id]);
      completeRequest();
      await deletion;
      expect(context.deletingRunnerIds).to.deep.equal([]);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });
});
