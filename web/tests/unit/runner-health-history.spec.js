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

  it('uses the existing health dialog for global Docker policy and diagnostics', async () => {
    const calls = [];
    const context = {
      runner: { id: 12, project_id: null, executor_type: 'docker' },
      dockerAdmin: true,
      loading: false,
      error: null,
      health: { heartbeat_state: 'online' },
      history: [{ task_id: 1 }],
      loadDockerPolicy: async () => calls.push('policy'),
      loadDockerDiagnostics: async (append) => calls.push(`diagnostics:${append}`),
    };

    await RunnerHealthDialog.methods.load.call(context);

    expect(calls).to.deep.equal(['policy', 'diagnostics:false']);
    expect(context.loading).to.equal(false);
    expect(context.health).to.equal(null);
    expect(context.history).to.deep.equal([]);
    expect(RunnerHealthDialog.computed.dockerPolicyAcknowledged.call({
      dockerPolicy: { revision: 4, hash: 'policy-hash' },
      runner: { docker_policy_revision: 4, docker_policy_hash: 'policy-hash' },
    })).to.equal(true);
    expect(RunnerHealthDialog.computed.dialogTitle.call({
      dockerAdmin: true,
      $t: () => 'Runner health and history',
    })).to.equal('Docker runner diagnostics');
  });

  it('uses the existing health dialog for global Kubernetes policy and remediation', async () => {
    const calls = [];
    const context = {
      runner: { id: 14, project_id: null, executor_type: 'k8s' },
      dockerAdmin: false,
      kubernetesAdmin: true,
      loading: false,
      error: null,
      health: { heartbeat_state: 'online' },
      history: [{ task_id: 1 }],
      loadKubernetesPolicy: async () => calls.push('policy'),
      loadKubernetesDiagnostics: async () => calls.push('diagnostics'),
    };

    await RunnerHealthDialog.methods.load.call(context);

    expect(calls).to.deep.equal(['policy', 'diagnostics']);
    expect(context.loading).to.equal(false);
    expect(context.health).to.equal(null);
    expect(RunnerHealthDialog.computed.dialogTitle.call({
      dockerAdmin: false,
      kubernetesAdmin: true,
      $t: () => 'Runner health and history',
    })).to.equal('Kubernetes runner diagnostics');
    expect(RunnerHealthDialog.computed.kubernetesPolicyAcknowledged.call({
      kubernetesPolicy: { cluster_alias: 'qa', revision: 4, hash: 'policy-hash' },
      runner: { k8s_cluster_alias: 'qa', k8s_policy_revision: 4, k8s_policy_hash: 'policy-hash' },
    })).to.equal(true);
  });

  it('loads Kubernetes policy and submits only a server-built remediation descriptor', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config);
      let data = { diagnostics: [] };
      if (config.method === 'post') {
        data = { status: 'pending' };
      } else if (config.url.includes('/kubernetes-policies/')) {
        data = { cluster_alias: 'qa', revision: 4, hash: 'policy-hash' };
      }
      return {
        data,
        status: config.method === 'post' ? 202 : 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const diagnostic = {
        target: { project_id: 7, task_id: 41, generation: 3 },
        state: 'observed',
        remediation: {
          action: 'garbage_collect_expired',
          project_id: 7,
          task_id: 41,
          generation: 3,
          expected_revision: 4,
        },
      };
      const key = 'target:7:41:3';
      const context = {
        runner: { id: 14, k8s_cluster_alias: 'qa' },
        kubernetesPolicy: null,
        kubernetesPolicyError: null,
        kubernetesDiagnostics: [],
        kubernetesDiagnosticsError: null,
        remediatingKubernetesDiagnostics: [],
        kubernetesRemediationKeys: { [key]: 'ui-stable-key' },
        kubernetesDiagnosticKey: RunnerHealthDialog.methods.kubernetesDiagnosticKey,
        kubernetesRemediationIdempotencyKey:
          RunnerHealthDialog.methods.kubernetesRemediationIdempotencyKey,
        loadKubernetesDiagnostics: RunnerHealthDialog.methods.loadKubernetesDiagnostics,
        $set: (target, property, value) => Reflect.set(target, property, value),
        $delete: (target, property) => Reflect.deleteProperty(target, property),
      };

      await RunnerHealthDialog.methods.loadKubernetesPolicy.call(context);
      await RunnerHealthDialog.methods.loadKubernetesDiagnostics.call(context);
      await RunnerHealthDialog.methods.requestKubernetesRemediation.call(context, diagnostic);

      expect(requests[0].url).to.equal('/api/runners/kubernetes-policies/qa');
      expect(requests[1].url)
        .to.equal('/api/runners/14/kubernetes-reconciliation/diagnostics');
      expect(requests[2].url)
        .to.equal('/api/runners/14/kubernetes-reconciliation/remediation');
      expect(JSON.parse(requests[2].data)).to.deep.equal({
        action: 'garbage_collect_expired',
        project_id: 7,
        task_id: 41,
        generation: 3,
        expected_revision: 4,
        idempotency_key: 'ui-stable-key',
      });
      expect(context.remediatingKubernetesDiagnostics).to.deep.equal([]);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('loads Docker policy and diagnostics from the existing global runner API', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config);
      return {
        data: config.url.endsWith('/docker-policy')
          ? { revision: 0, hash: 'policy-hash' }
          : { diagnostics: [], next_cursor: null },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        runner: { id: 12 },
        dockerPolicy: null,
        dockerPolicyError: null,
        dockerDiagnostics: [],
        dockerDiagnosticsNextCursor: null,
        dockerDiagnosticsError: null,
      };

      await RunnerHealthDialog.methods.loadDockerPolicy.call(context);
      await RunnerHealthDialog.methods.loadDockerDiagnostics.call(context, false);

      expect(requests.map(({ url }) => url)).to.deep.equal([
        '/api/runners/docker-policy',
        '/api/runners/12/docker-reconciliation/diagnostics',
      ]);
      expect(requests[1].params).to.deep.equal({ limit: 25 });
      expect(context.dockerPolicy.revision).to.equal(0);
      expect(context.dockerDiagnostics).to.deep.equal([]);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('saves a bounded full Docker policy through the existing global runner API', async () => {
    const previousAdapter = axios.defaults.adapter;
    let request;
    axios.defaults.adapter = async (config) => {
      request = config;
      return {
        data: { revision: 5, hash: 'saved' },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        dockerPolicyDraft: {
          revision: 4,
          allowedImagesText: 'registry.test/task@sha256:abc\n\n registry.test/helper@sha256:def ',
          allowed_images: [],
          allowed_networks: ['none', 'internal'],
          network: 'bridge',
          require_digest: true,
        },
        savingDockerPolicy: false,
        dockerPolicyError: null,
        editingDockerPolicy: true,
        dockerPolicy: null,
      };

      await RunnerHealthDialog.methods.saveDockerPolicy.call(context);

      expect(request.method).to.equal('put');
      expect(request.url).to.equal('/api/runners/docker-policy');
      const payload = JSON.parse(request.data);
      expect(payload.allowed_images).to.deep.equal([
        'registry.test/task@sha256:abc',
        'registry.test/helper@sha256:def',
      ]);
      expect(payload.allowed_networks).to.deep.equal(['none', 'internal', 'bridge']);
      expect(payload).not.to.have.property('allowedImagesText');
      expect(context.dockerPolicy.revision).to.equal(5);
      expect(context.editingDockerPolicy).to.equal(false);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('submits only the server-built Docker remediation target', async () => {
    const previousAdapter = axios.defaults.adapter;
    let request;
    axios.defaults.adapter = async (config) => {
      request = config;
      return {
        data: { status: 'pending' },
        status: 202,
        statusText: 'Accepted',
        headers: {},
        config,
      };
    };

    try {
      const diagnostic = {
        type: 'candidate',
        remediation: {
          target: 'candidate',
          expected_revision: 3,
          candidate: { session_id: 'session-a', fingerprint: 'f'.repeat(64) },
        },
      };
      const key = 'candidate:session-a:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff';
      let reloads = 0;
      const context = {
        runner: { id: 12 },
        remediatingDockerDiagnostics: [],
        dockerDiagnosticsError: null,
        dockerRemediationKeys: { [key]: 'ui-stable-key' },
        dockerDiagnosticKey: RunnerHealthDialog.methods.dockerDiagnosticKey,
        dockerRemediationIdempotencyKey:
          RunnerHealthDialog.methods.dockerRemediationIdempotencyKey,
        loadDockerDiagnostics: async () => { reloads += 1; },
        $set: (target, property, value) => Reflect.set(target, property, value),
        $delete: (target, property) => Reflect.deleteProperty(target, property),
      };

      await RunnerHealthDialog.methods.requestDockerRemediation.call(context, diagnostic);

      expect(request.url)
        .to.equal('/api/runners/12/docker-reconciliation/remediation');
      expect(JSON.parse(request.data)).to.deep.equal({
        action: 'retry_stop_and_cleanup',
        idempotency_key: 'ui-stable-key',
        expected_revision: 3,
        target: 'candidate',
        session_id: 'session-a',
        fingerprint: 'f'.repeat(64),
      });
      expect(reloads).to.equal(1);
      expect(context.remediatingDockerDiagnostics).to.deep.equal([]);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });
});
