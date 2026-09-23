import assert from 'node:assert/strict';
import test from 'node:test';

import {
  createGhTransport,
  verifyReleaseCi,
  verifyWorkflowEvidence,
  workflowRequirements,
} from './verify-ci.mjs';

const SHA = '02d45cfedc9813ca6a7fcae8035941b2f201b6a5';
const REPOSITORY = 'freefair/semaphore-ex';

function workflow(overrides = {}) {
  return {
    id: 42,
    name: 'Dev',
    path: '.github/workflows/dev.yml',
    state: 'active',
    ...overrides,
  };
}

function run(overrides = {}) {
  return {
    id: 100,
    workflow_id: 42,
    run_attempt: 1,
    event: 'push',
    head_branch: 'develop',
    head_sha: SHA,
    status: 'completed',
    conclusion: 'success',
    created_at: '2026-09-23T10:00:00Z',
    updated_at: '2026-09-23T10:10:00Z',
    html_url: 'https://github.com/freefair/semaphore-ex/actions/runs/100',
    repository: { full_name: REPOSITORY },
    head_repository: { full_name: REPOSITORY },
    ...overrides,
  };
}

function successfulJobs(requiredJobs) {
  return requiredJobs.map((name, index) => ({
    id: index + 1,
    name,
    status: 'completed',
    conclusion: 'success',
  }));
}

function evidence(overrides = {}) {
  const {
    run: runOverride,
    workflow: workflowOverride,
    attempt: attemptOverride,
    runs,
    jobs,
    ...directOverrides
  } = overrides;
  const requirements = workflowRequirements().dev;
  const candidate = run(runOverride);
  return {
    ...directOverrides,
    expectedRepository: REPOSITORY,
    expectedSha: SHA,
    repository: directOverrides.repository ?? { full_name: REPOSITORY },
    workflow: workflow(workflowOverride),
    expectedWorkflow: requirements,
    runs: runs ?? [candidate],
    attempt: { ...candidate, ...(attemptOverride ?? {}) },
    currentRun: { ...candidate, ...(directOverrides.currentRun ?? {}) },
    jobs: jobs ?? successfulJobs(requirements.requiredJobs),
  };
}

test('accepts a current successful exact-SHA Dev run with all required jobs', () => {
  const result = verifyWorkflowEvidence(evidence());

  assert.deepEqual(result, {
    workflowId: 42,
    workflowName: 'Dev',
    runId: 100,
    attempt: 1,
    headSha: SHA,
    url: 'https://github.com/freefair/semaphore-ex/actions/runs/100',
  });
});

test('rejects workflow evidence that belongs to another repository', () => {
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ repository: { full_name: 'fork/semaphore-ex' } })),
    /repository metadata/,
  );
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ run: { head_repository: { full_name: 'fork/semaphore-ex' } } })),
    /head repository/,
  );
});

test('rejects wrong SHA, event, and branch run metadata', () => {
  for (const [label, override] of Object.entries({
    sha: { head_sha: 'bad-sha' },
    event: { event: 'pull_request' },
    branch: { head_branch: 'feature' },
  })) {
    assert.throws(
      () => verifyWorkflowEvidence(evidence({ run: override })),
      new RegExp(label === 'sha' ? 'no matching' : 'no matching'),
      label,
    );
  }
});

test('rejects mismatched workflow path and inactive workflow', () => {
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ workflow: { path: '.github/workflows/other.yml' } })),
    /path/,
  );
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ workflow: { state: 'disabled_manually' } })),
    /active/,
  );
});

test('uses the newest matching run and rejects a new failure or pending run', () => {
  const prior = run({ id: 99, created_at: '2026-09-23T09:00:00Z' });
  const failed = run({ id: 101, conclusion: 'failure', created_at: '2026-09-23T11:00:00Z' });
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ runs: [prior, failed] })),
    /conclusion is failure/,
  );

  const pending = run({ id: 101, status: 'in_progress', conclusion: null, created_at: '2026-09-23T11:00:00Z' });
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ runs: [prior, pending] })),
    /status is in_progress/,
  );
});

test('rejects an API error and missing required job', () => {
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ apiError: new Error('request failed') })),
    /GitHub API request failed/,
  );
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ jobs: [] })),
    /required job build-local/,
  );
});

test('fails closed when the GitHub API transport errors', async () => {
  const transport = createGhTransport({
    execFileImpl: async () => {
      throw new Error('network unavailable');
    },
  });

  await assert.rejects(transport.get('repos/freefair/semaphore-ex'), /GitHub API request failed/);
  await assert.rejects(transport.getAll('repos/freefair/semaphore-ex/actions/runs'), /GitHub API request failed/);
});

test('rejects a changed failed current attempt even if its run summary was successful', () => {
  assert.throws(
    () => verifyWorkflowEvidence(evidence({ attempt: { conclusion: 'failure' } })),
    /attempt conclusion is failure/,
  );
});

test('accepts Full Product Build only when its artifact and HA jobs succeeded', () => {
  const requirements = workflowRequirements().product;
  const productWorkflow = {
    id: 43,
    name: requirements.name,
    path: requirements.path,
    state: 'active',
  };
  const productRun = run({ id: 101, workflow_id: 43 });
  const productEvidence = {
    expectedRepository: REPOSITORY,
    expectedSha: SHA,
    repository: { full_name: REPOSITORY },
    workflow: productWorkflow,
    expectedWorkflow: requirements,
    runs: [productRun],
    attempt: productRun,
    currentRun: productRun,
    jobs: successfulJobs(requirements.requiredJobs),
  };

  assert.equal(verifyWorkflowEvidence(productEvidence).workflowName, 'Full Product Build');
  assert.throws(
    () => verifyWorkflowEvidence({
      ...productEvidence,
      jobs: [{ ...successfulJobs(requirements.requiredJobs)[0], conclusion: 'failure' }],
    }),
    /Full-product artifacts conclusion is failure/,
  );
});

test('fails closed when a rerun starts after successful attempt jobs were read', async () => {
  const requirements = workflowRequirements().dev;
  const selected = run();
  const transport = {
    async get(endpoint) {
      if (endpoint === `repos/${REPOSITORY}/actions/workflows/${encodeURIComponent(requirements.path)}`) {
        return workflow();
      }
      if (endpoint === `repos/${REPOSITORY}/actions/runs/${selected.id}/attempts/${selected.run_attempt}`) {
        return selected;
      }
      if (endpoint === `repos/${REPOSITORY}/actions/runs/${selected.id}`) {
        return { ...selected, run_attempt: 2, status: 'in_progress', conclusion: null };
      }
      if (endpoint === `repos/${REPOSITORY}`) {
        return { full_name: REPOSITORY };
      }
      throw new Error(`unexpected GET ${endpoint}`);
    },
    async getAll(endpoint) {
      if (endpoint.includes('/runs?')) {
        assert.match(endpoint, new RegExp(`head_sha=${SHA}`));
        return [{ workflow_runs: [selected] }];
      }
      if (endpoint.includes('/jobs?')) {
        return [{ jobs: successfulJobs(requirements.requiredJobs) }];
      }
      throw new Error(`unexpected paginated GET ${endpoint}`);
    },
  };

  await assert.rejects(
    verifyReleaseCi({ transport, repository: REPOSITORY, releaseSha: SHA }),
    /current run attempt number changed/,
  );
});
