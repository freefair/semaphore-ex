import { expect } from 'chai';
import axios from 'axios';
import TaskDetails from '@/components/TaskDetails.vue';

describe('runner reconciliation task details', () => {
  it('formats immutable runner attempts and their terminal outcome', () => {
    const attempt = { runner_id: 7, runner_name: 'recovery runner', outcome: 'requeued' };

    expect(TaskDetails.methods.runnerAttemptIdentity(attempt))
      .to.equal('#7 — recovery runner');
    expect(TaskDetails.methods.runnerAttemptLabel(attempt.outcome)).to.equal('Requeued');
    expect(TaskDetails.methods.runnerAttemptColor(attempt.outcome)).to.equal('warning');
    expect(TaskDetails.watch.item.deep).to.equal(true);
  });

  it('loads the task-scoped attempt history alongside template details', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config.url);
      return {
        data: [{
          task_id: 41,
          generation: 2,
          runner_id: 7,
          runner_name: 'recovery runner',
          outcome: 'succeeded',
        }],
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        projectId: 3,
        item: {
          id: 41, template_id: 9, status: 'success', assignment_generation: 2,
        },
        template: null,
        runnerAttempts: [],
        runnerAttemptsError: null,
        loadedTaskId: null,
        loadedTaskStatus: null,
        loadedAssignmentGeneration: null,
        loadRevision: 0,
        loadProjectResource: async (_resource, id) => ({ id, name: 'template' }),
        loadRunnerAttempts: TaskDetails.methods.loadRunnerAttempts,
      };

      await TaskDetails.methods.loadData.call(context);

      expect(context.template).to.deep.equal({ id: 9, name: 'template' });
      expect(context.runnerAttempts).to.have.length(1);
      expect(context.runnerAttempts[0].generation).to.equal(2);
      expect(context.loadedTaskId).to.equal(41);
      expect(context.loadedTaskStatus).to.equal('success');
      expect(context.loadedAssignmentGeneration).to.equal(2);
      expect(context.loadRevision).to.equal(1);
      expect(requests).to.deep.equal(['/api/project/3/tasks/41/runner-attempts']);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('keeps task details usable when attempt history is unavailable', async () => {
    const previousAdapter = axios.defaults.adapter;
    axios.defaults.adapter = async () => {
      throw new Error('offline');
    };

    try {
      const context = {
        projectId: 3,
        runnerAttemptsError: null,
      };
      const result = await TaskDetails.methods.loadRunnerAttempts.call(context, 41);

      expect(result.attempts).to.deep.equal([]);
      expect(result.error).to.equal('Runner attempt history could not be loaded.');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('refreshes attempts when a live task reaches another status', async () => {
    let reloads = 0;
    const context = {
      loadedTaskId: 41,
      loadedTaskStatus: 'running',
      loadedAssignmentGeneration: 2,
      template: { id: 9 },
      loadData: async () => { reloads += 1; },
    };

    await TaskDetails.watch.item.handler.call(context, {
      id: 41, template_id: 9, status: 'success', assignment_generation: 2,
    });

    expect(reloads).to.equal(1);
  });
});
