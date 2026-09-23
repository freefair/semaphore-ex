import { execFile } from 'node:child_process';
import { resolve } from 'node:path';
import process from 'node:process';
import { promisify } from 'node:util';
import { pathToFileURL } from 'node:url';

const execFileAsync = promisify(execFile);

const WORKFLOWS = Object.freeze({
  dev: Object.freeze({
    name: 'Dev',
    path: '.github/workflows/dev.yml',
    requiredJobs: Object.freeze([
      'build-local',
      'migrate-mysql',
      'migrate-mariadb',
      'migrate-sqlite',
      'migrate-postgres',
      'integrate-mysql',
      'integrate-mariadb',
      'integrate-sqlite',
      'integrate-postgres',
    ]),
  }),
  product: Object.freeze({
    name: 'Full Product Build',
    path: '.github/workflows/product_build.yml',
    requiredJobs: Object.freeze(['Full-product artifacts', 'HA resilience contract']),
  }),
});

export function workflowRequirements() {
  return WORKFLOWS;
}

function fail(message) {
  throw new Error(`Release CI verification failed: ${message}`);
}

function expect(value, message) {
  if (!value) {
    fail(message);
  }
}

function isObject(value) {
  return value !== null && typeof value === 'object';
}

function sameRepository(value, expectedRepository) {
  return isObject(value) && value.full_name === expectedRepository;
}

function validateWorkflow(workflow, expected) {
  expect(isObject(workflow), `${expected.name} workflow response is invalid`);
  expect(Number.isInteger(workflow.id) && workflow.id > 0, `${expected.name} workflow id is invalid`);
  expect(workflow.name === expected.name, `${expected.name} workflow name does not match`);
  expect(workflow.path === expected.path, `${expected.name} workflow path does not match`);
  expect(workflow.state === 'active', `${expected.name} workflow is not active`);
}

function runCreatedAt(run) {
  const timestamp = Date.parse(run.created_at);
  expect(Number.isFinite(timestamp), `workflow run ${run.id ?? 'unknown'} has an invalid creation time`);
  return timestamp;
}

function runId(run) {
  expect(Number.isInteger(run.id) && run.id > 0, 'workflow run id is invalid');
  return run.id;
}

function matchingRuns(runs, workflow, expectedSha) {
  expect(Array.isArray(runs), `${workflow.name} workflow runs response is invalid`);
  return runs
    .filter((run) => isObject(run)
      && run.workflow_id === workflow.id
      && run.event === 'push'
      && run.head_branch === 'develop'
      && run.head_sha === expectedSha)
    .sort((left, right) => runCreatedAt(right) - runCreatedAt(left) || runId(right) - runId(left));
}

function validateRunBinding(run, { expectedRepository, expectedSha, workflow, label }) {
  expect(isObject(run), `${workflow.name} ${label} response is invalid`);
  expect(Number.isInteger(run.id) && run.id > 0, `${workflow.name} ${label} id is invalid`);
  expect(run.workflow_id === workflow.id, `${workflow.name} ${label} workflow id does not match`);
  expect(run.event === 'push', `${workflow.name} ${label} event is not push`);
  expect(run.head_branch === 'develop', `${workflow.name} ${label} branch is not develop`);
  expect(run.head_sha === expectedSha, `${workflow.name} ${label} SHA does not match the release commit`);
  expect(sameRepository(run.repository, expectedRepository), `${workflow.name} ${label} repository does not match`);
  expect(sameRepository(run.head_repository, expectedRepository), `${workflow.name} ${label} head repository does not match`);
}

function validateConclusion(subject, workflow, label) {
  expect(subject.status === 'completed', `${workflow.name} ${label} status is ${subject.status ?? 'missing'}`);
  expect(subject.conclusion === 'success', `${workflow.name} ${label} conclusion is ${subject.conclusion ?? 'missing'}`);
}

function validateAttempt(attempt, selectedRun, context) {
  validateRunBinding(attempt, { ...context, label: 'attempt' });
  expect(attempt.id === selectedRun.id, `${context.workflow.name} attempt id does not match selected run`);
  expect(attempt.run_attempt === selectedRun.run_attempt, `${context.workflow.name} attempt number changed`);
  validateConclusion(attempt, context.workflow, 'attempt');
}

function validateCurrentRun(currentRun, selectedRun, context) {
  validateRunBinding(currentRun, { ...context, label: 'current run' });
  expect(currentRun.id === selectedRun.id, `${context.workflow.name} current run id does not match selected run`);
  expect(currentRun.run_attempt === selectedRun.run_attempt, `${context.workflow.name} current run attempt number changed`);
  validateConclusion(currentRun, context.workflow, 'current run');
}

function validateRequiredJobs(jobs, expected) {
  expect(Array.isArray(jobs), `${expected.name} jobs response is invalid`);
  for (const requiredJob of expected.requiredJobs) {
    const matchingJobs = jobs.filter((candidate) => isObject(candidate) && candidate.name === requiredJob);
    expect(matchingJobs.length === 1, `${expected.name} required job ${requiredJob} is missing or duplicated`);
    const [job] = matchingJobs;
    expect(job.status === 'completed', `${expected.name} required job ${requiredJob} status is ${job.status ?? 'missing'}`);
    expect(job.conclusion === 'success', `${expected.name} required job ${requiredJob} conclusion is ${job.conclusion ?? 'missing'}`);
  }
}

/**
 * Validate the immutable API evidence for one required workflow.
 *
 * This function is deliberately pure so fixture tests exercise the same binding
 * checks used by the GitHub Actions entrypoint.
 */
export function verifyWorkflowEvidence(evidence) {
  if (evidence.apiError) {
    fail('GitHub API request failed');
  }

  const {
    expectedRepository,
    expectedSha,
    repository,
    workflow,
    expectedWorkflow,
    runs,
    attempt,
    currentRun,
    jobs,
  } = evidence;

  expect(typeof expectedRepository === 'string' && expectedRepository.length > 0, 'expected repository is missing');
  expect(typeof expectedSha === 'string' && /^[0-9a-f]{40}$/i.test(expectedSha), 'release commit SHA is invalid');
  expect(sameRepository(repository, expectedRepository), 'repository metadata does not match the checkout repository');
  validateWorkflow(workflow, expectedWorkflow);

  const candidates = matchingRuns(runs, workflow, expectedSha);
  expect(candidates.length > 0, `${workflow.name} has no matching push run for the release commit on develop`);
  const newest = candidates[0];
  validateRunBinding(newest, {
    expectedRepository,
    expectedSha,
    workflow,
    label: 'newest run',
  });
  validateConclusion(newest, workflow, 'newest run');
  expect(Number.isInteger(newest.run_attempt) && newest.run_attempt > 0, `${workflow.name} newest run attempt is invalid`);
  validateAttempt(attempt, newest, { expectedRepository, expectedSha, workflow });
  validateCurrentRun(currentRun, newest, { expectedRepository, expectedSha, workflow });
  validateRequiredJobs(jobs, expectedWorkflow);

  return {
    workflowId: workflow.id,
    workflowName: workflow.name,
    runId: newest.id,
    attempt: newest.run_attempt,
    headSha: newest.head_sha,
    url: newest.html_url,
  };
}

function parseJson(stdout, endpoint) {
  try {
    return JSON.parse(stdout);
  } catch {
    fail(`GitHub API returned invalid JSON for ${endpoint}`);
  }
}

export function createGhTransport({ execFileImpl = execFileAsync } = {}) {
  return {
    async get(endpoint) {
      try {
        const { stdout } = await execFileImpl('gh', ['api', '--method', 'GET', endpoint], { encoding: 'utf8' });
        return parseJson(stdout, endpoint);
      } catch (error) {
        if (error instanceof SyntaxError) {
          throw error;
        }
        fail(`GitHub API request failed for ${endpoint}`);
      }
    },
    async getAll(endpoint) {
      try {
        const { stdout } = await execFileImpl('gh', ['api', '--method', 'GET', endpoint, '--paginate', '--slurp'], { encoding: 'utf8' });
        const pages = parseJson(stdout, endpoint);
        expect(Array.isArray(pages), `GitHub API paginated response is invalid for ${endpoint}`);
        return pages;
      } catch (error) {
        if (error instanceof Error && error.message.startsWith('Release CI verification failed:')) {
          throw error;
        }
        fail(`GitHub API request failed for ${endpoint}`);
      }
    },
  };
}

function flattenPaginated(pages, key, description) {
  expect(Array.isArray(pages), `${description} paginated response is invalid`);
  return pages.flatMap((page) => {
    expect(isObject(page) && Array.isArray(page[key]), `${description} page is invalid`);
    return page[key];
  });
}

export async function resolveCheckoutCommit({ execFileImpl = execFileAsync, env = process.env } = {}) {
  // This affects SSH remotes only; HTTPS checkouts in GitHub Actions ignore it.
  const gitEnv = { ...env, GIT_SSH_COMMAND: 'ssh -o IdentitiesOnly=yes' };
  const executeGit = async (argumentsList) => {
    try {
      const { stdout } = await execFileImpl('git', argumentsList, { encoding: 'utf8', env: gitEnv });
      return stdout.trim();
    } catch {
      fail(`git ${argumentsList[0]} failed while resolving release commit`);
    }
  };

  const checkoutSha = await executeGit(['rev-parse', 'HEAD^{commit}']);
  const releaseSha = env.GITHUB_REF_TYPE === 'tag'
    ? await executeGit(['rev-parse', `refs/tags/${env.GITHUB_REF_NAME}^{commit}`])
    : checkoutSha;
  expect(/^[0-9a-f]{40}$/i.test(releaseSha), 'resolved release commit SHA is invalid');
  expect(checkoutSha === releaseSha, 'checkout commit does not match the release ref');

  await executeGit(['fetch', '--no-tags', 'origin', '+refs/heads/develop:refs/remotes/origin/develop']);
  try {
    await execFileImpl('git', ['merge-base', '--is-ancestor', releaseSha, 'origin/develop'], {
      encoding: 'utf8',
      env: gitEnv,
    });
  } catch {
    fail('release commit is not reachable from origin/develop');
  }

  return releaseSha;
}

async function collectWorkflowEvidence({ transport, repository, releaseSha, expectedWorkflow }) {
  const encodedRepository = repository.split('/').map(encodeURIComponent).join('/');
  const workflow = await transport.get(`repos/${encodedRepository}/actions/workflows/${encodeURIComponent(expectedWorkflow.path)}`);
  validateWorkflow(workflow, expectedWorkflow);

  const runPages = await transport.getAll(
    `repos/${encodedRepository}/actions/workflows/${workflow.id}/runs?event=push&branch=develop&head_sha=${encodeURIComponent(releaseSha)}&per_page=100`,
  );
  const runs = flattenPaginated(runPages, 'workflow_runs', `${expectedWorkflow.name} workflow runs`);
  const candidates = matchingRuns(runs, workflow, releaseSha);
  expect(candidates.length > 0, `${expectedWorkflow.name} has no matching push run for the release commit on develop`);
  const newest = candidates[0];
  validateRunBinding(newest, {
    expectedRepository: repository,
    expectedSha: releaseSha,
    workflow,
    label: 'newest run',
  });
  validateConclusion(newest, workflow, 'newest run');
  expect(Number.isInteger(newest.run_attempt) && newest.run_attempt > 0, `${expectedWorkflow.name} newest run attempt is invalid`);

  const attempt = await transport.get(
    `repos/${encodedRepository}/actions/runs/${newest.id}/attempts/${newest.run_attempt}`,
  );
  const jobPages = await transport.getAll(
    `repos/${encodedRepository}/actions/runs/${newest.id}/attempts/${newest.run_attempt}/jobs?per_page=100`,
  );
  const jobs = flattenPaginated(jobPages, 'jobs', `${expectedWorkflow.name} jobs`);
  const currentRun = await transport.get(`repos/${encodedRepository}/actions/runs/${newest.id}`);

  return verifyWorkflowEvidence({
    expectedRepository: repository,
    expectedSha: releaseSha,
    repository: await transport.get(`repos/${encodedRepository}`),
    workflow,
    expectedWorkflow,
    runs,
    attempt,
    currentRun,
    jobs,
  });
}

export async function verifyReleaseCi({
  transport = createGhTransport(),
  repository = process.env.GITHUB_REPOSITORY,
  releaseSha,
} = {}) {
  expect(typeof repository === 'string' && /^[^/]+\/[^/]+$/.test(repository), 'GITHUB_REPOSITORY is invalid');
  const resolvedReleaseSha = releaseSha ?? await resolveCheckoutCommit();
  const results = [];
  for (const expectedWorkflow of Object.values(WORKFLOWS)) {
    results.push(await collectWorkflowEvidence({
      transport,
      repository,
      releaseSha: resolvedReleaseSha,
      expectedWorkflow,
    }));
  }
  return { releaseSha: resolvedReleaseSha, workflows: results };
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  verifyReleaseCi()
    .then((summary) => {
      console.log(JSON.stringify(summary));
    })
    .catch((error) => {
      console.error(error.message);
      process.exitCode = 1;
    });
}
