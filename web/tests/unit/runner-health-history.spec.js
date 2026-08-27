import { expect } from 'chai';
import axios from 'axios';
import RunnerHealthDialog from '@/components/RunnerHealthDialog.vue';
import TaskDetails from '@/components/TaskDetails.vue';
import Runners from '@/views/Runners.vue';

describe('project runner health and history', () => {
  it('opens the health panel for the selected runner', () => {
    const runner = { id: 7, name: 'selected' };
    const context = { selectedHealthRunner: null, runnerHealthDialog: false };

    Runners.methods.openRunnerHealth.call(context, runner);

    expect(context.selectedHealthRunner).to.equal(runner);
    expect(context.runnerHealthDialog).to.equal(true);
  });

  it('keeps the non-secret runner id and name visible in task details', () => {
    const identity = TaskDetails.computed.runnerIdentity.call({
      item: { used_runner_id: 7, used_runner_name: 'deleted runner' },
    });

    expect(identity).to.equal('#7 — deleted runner');
  });

  it('renders explicit heartbeat messages and deterministic uptime', () => {
    const $t = (key, values = {}) => `${key}:${values.age ?? ''}:${values.boundary ?? ''}`;
    const offline = {
      health: { heartbeat_state: 'offline', heartbeat_age_seconds: 181, heartbeat_timeout_seconds: 120 },
      $t,
    };
    expect(RunnerHealthDialog.computed.heartbeatMessage.call(offline))
      .to.equal('runnerHeartbeatStale:181:120');
    expect(RunnerHealthDialog.methods.formatUptime.call({ $t }, 93780)).to.equal('1d 2h 3m');
    expect(RunnerHealthDialog.methods.formatUptime.call({ $t }, null)).to.equal('notReported::');
  });

  it('loads scoped health and appends cursor-paginated non-secret history', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config);
      if (config.url.endsWith('/health')) {
        return {
          data: { heartbeat_state: 'online', current_load: 1 },
          status: 200,
          statusText: 'OK',
          headers: {},
          config,
        };
      }
      const before = config.params?.before;
      return {
        data: before
          ? { items: [{ task_id: 8, runner_id: 7, runner_name: 'runner' }], has_more: false }
          : {
            items: [{ task_id: 9, runner_id: 7, runner_name: 'runner' }],
            has_more: true,
            next_before: 9,
          },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        projectId: 3,
        runner: { id: 7 },
        health: null,
        history: [],
        hasMore: false,
        nextBefore: null,
        loading: false,
        historyLoading: false,
        error: null,
      };
      await RunnerHealthDialog.methods.load.call(context);
      expect(context.health.heartbeat_state).to.equal('online');
      expect(context.history.map((item) => item.task_id)).to.deep.equal([9]);
      expect(context.nextBefore).to.equal(9);

      await RunnerHealthDialog.methods.loadMore.call(context);
      expect(context.history.map((item) => item.task_id)).to.deep.equal([9, 8]);
      expect(context.hasMore).to.equal(false);
      expect(requests.map((request) => request.url)).to.deep.equal([
        '/api/project/3/runners/7/health',
        '/api/project/3/runners/7/history',
        '/api/project/3/runners/7/history',
      ]);
      expect(JSON.stringify(context)).not.to.include('token');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });
});
