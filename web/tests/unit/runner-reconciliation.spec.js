import { expect } from 'chai';
import axios from 'axios';
import TaskRunnerDetails from '@/components/TaskRunnerDetails.vue';

describe('runner reconciliation task details', () => {
  it('formats immutable runner attempts and their terminal outcome', () => {
    const attempt = {
      runner_id: 7,
      runner_name: 'recovery runner',
      outcome: 'requeued',
      executor_type: 'docker',
      container_name: 'semaphore-task-41-boot',
      container_id: 'abc123',
    };

    expect(TaskRunnerDetails.methods.runnerAttemptIdentity(attempt))
      .to.equal('#7 — recovery runner');
    expect(TaskRunnerDetails.methods.runnerAttemptLabel(attempt.outcome)).to.equal('Requeued');
    expect(TaskRunnerDetails.methods.runnerAttemptColor(attempt.outcome)).to.equal('warning');
    expect(TaskRunnerDetails.methods.runnerAttemptExecutorLabel(attempt)).to.equal('docker');
    expect(TaskRunnerDetails.methods.runnerAttemptExecutorLabel({})).to.equal('local');
    expect(TaskRunnerDetails.methods.recoveryDecisionColor('quarantine')).to.equal('warning');
    expect(TaskRunnerDetails.watch.item.deep).to.equal(true);
  });

  it('loads the task-scoped attempt history', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config.url);
      return {
        data: config.url.endsWith('/recovery') ? {
          controlled: true,
          owner_boot_id: 'boot-b',
          previous_owner_boot_id: 'boot-a',
          evidence_state: 'terminal',
          recovery_decision: 'recover',
        } : [{
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
        runnerAttempts: [],
        runnerAttemptsError: null,
        recoveryDiagnostics: null,
        recoveryDiagnosticsError: null,
        loadedTaskId: null,
        loadedTaskStatus: null,
        loadedAssignmentGeneration: null,
        loadRevision: 0,
        loadTaskRecoveryDiagnostics:
          TaskRunnerDetails.methods.loadTaskRecoveryDiagnostics,
      };

      await TaskRunnerDetails.methods.loadRunnerAttempts.call(context);

      expect(context.runnerAttempts).to.have.length(1);
      expect(context.runnerAttempts[0].generation).to.equal(2);
      expect(context.loadedTaskId).to.equal(41);
      expect(context.loadedTaskStatus).to.equal('success');
      expect(context.loadedAssignmentGeneration).to.equal(2);
      expect(context.loadRevision).to.equal(1);
      expect(context.recoveryDiagnostics.previous_owner_boot_id).to.equal('boot-a');
      expect(requests).to.deep.equal([
        '/api/project/3/tasks/41/runner-attempts',
        '/api/project/3/tasks/41/recovery',
      ]);
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
        item: { id: 41, status: 'running', assignment_generation: 1 },
        runnerAttempts: [],
        runnerAttemptsError: null,
        recoveryDiagnostics: null,
        recoveryDiagnosticsError: null,
        loadRevision: 0,
        loadTaskRecoveryDiagnostics:
          TaskRunnerDetails.methods.loadTaskRecoveryDiagnostics,
      };
      await TaskRunnerDetails.methods.loadRunnerAttempts.call(context);

      expect(context.runnerAttempts).to.deep.equal([]);
      expect(context.runnerAttemptsError)
        .to.equal('Runner attempt history could not be loaded.');
      expect(context.recoveryDiagnosticsError)
        .to.equal('HA task recovery diagnostics could not be loaded.');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('retries only the server-declared safe recovery action', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(`${config.method}:${config.url}`);
      return {
        data: null,
        status: 204,
        statusText: 'No Content',
        headers: {},
        config,
      };
    };

    try {
      let reloads = 0;
      const context = {
        projectId: 3,
        item: { id: 41 },
        retryingRecovery: false,
        recoveryActionError: 'old error',
        loadRunnerAttempts: async () => { reloads += 1; },
      };

      await TaskRunnerDetails.methods.retryTaskRecovery.call(context);

      expect(requests).to.deep.equal(['post:/api/project/3/tasks/41/retry-recovery']);
      expect(reloads).to.equal(1);
      expect(context.retryingRecovery).to.equal(false);
      expect(context.recoveryActionError).to.equal(null);
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
      loadRunnerAttempts: async () => { reloads += 1; },
    };

    await TaskRunnerDetails.watch.item.handler.call(context, {
      id: 41, template_id: 9, status: 'success', assignment_generation: 2,
    });

    expect(reloads).to.equal(1);
  });
});
