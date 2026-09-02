import { expect } from 'chai';
import axios from 'axios';
import TaskRunnerDetails from '@/components/TaskRunnerDetails.vue';
import TaskStatus from '@/components/TaskStatus.vue';

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
    expect(TaskRunnerDetails.methods.dockerPolicyReference({
      docker_policy_revision: 7,
      docker_policy_hash: 'sha256-policy',
    })).to.equal('revision 7 · sha256-policy');
    expect(TaskRunnerDetails.methods.dockerResourceLimits({
      docker_nano_cpus: 1500000000,
      docker_memory_bytes: 536870912,
      docker_pids_limit: 256,
    })).to.equal('1.5 CPU · 512 MiB · 256 PIDs');
    expect(TaskRunnerDetails.methods.kubernetesLocation({
      k8s_cluster_alias: 'qa-cluster',
      k8s_namespace: 'semaphore-jobs',
    })).to.equal('qa-cluster · semaphore-jobs');
    expect(TaskRunnerDetails.methods.kubernetesRuntimeIdentity({
      k8s_job_name: 'semaphore-task-41-3',
      k8s_pod_name: 'semaphore-task-41-3-b7d9f',
    })).to.equal('Job semaphore-task-41-3 · Pod semaphore-task-41-3-b7d9f');
    expect(TaskRunnerDetails.methods.kubernetesPolicyReference({
      k8s_policy_revision: 4,
      k8s_policy_hash: 'policy-hash',
    })).to.equal('revision 4 · policy-hash');
    expect(TaskRunnerDetails.methods.kubernetesWorkloadPolicy({
      k8s_service_account: 'semaphore-task',
      k8s_network_profile: 'deny-all',
      k8s_network_enforcement: 'network-policy',
      k8s_resource_policy_id: '4',
    })).to.equal('service account semaphore-task · network deny-all (network-policy) · resources 4');
    expect(TaskRunnerDetails.methods.kubernetesResourceIdentities({
      k8s_secret_name: 'semaphore-bundle-41-3',
      k8s_secret_uid: 'bundle-uid',
      k8s_network_policy_name: 'semaphore-network-41-3',
      k8s_network_policy_uid: 'network-uid',
    })).to.equal('Bundle semaphore-bundle-41-3 (bundle-uid) · NetworkPolicy semaphore-network-41-3 (network-uid)');
    expect(TaskRunnerDetails.methods.kubernetesRetention({
      k8s_retention_state: 'terminal',
      k8s_retention_deadline: '2026-08-31T12:00:00Z',
    })).to.equal('terminal · until 2026-08-31T12:00:00Z');
    expect(TaskRunnerDetails.methods.recoveryDecisionColor('quarantine')).to.equal('warning');
    expect(TaskRunnerDetails.methods.shortCredentialFingerprint('abcdef1234567890'))
      .to.equal('abcdef123456…');
    expect(TaskRunnerDetails.methods.credentialOutcomeColor('denied')).to.equal('warning');
    expect(TaskRunnerDetails.watch.item.deep).to.equal(true);
  });

  it('renders blocked tasks as an actionable terminal status', () => {
    expect(TaskStatus.methods.getStatusIcon('blocked')).to.equal('mdi-lock-alert');
    expect(TaskStatus.methods.humanizeStatus('blocked')).to.equal('Blocked');
    expect(TaskStatus.methods.getStatusColor('blocked')).to.equal('warning');
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
