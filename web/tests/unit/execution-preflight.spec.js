import { expect } from 'chai';
import axios from 'axios';
import NewTaskDialog from '@/components/NewTaskDialog.vue';
import TaskForm from '@/components/TaskForm.vue';
import WorkflowRunDialog from '@/components/WorkflowRunDialog.vue';
import ExecutionPreflightReview from '@/components/ExecutionPreflightReview.vue';

describe('execution preflight review', () => {
  let originalPost;

  beforeEach(() => {
    originalPost = axios.post;
  });

  afterEach(() => {
    axios.post = originalPost;
  });

  it('reviews a task before forwarding the same payload with its review headers', async () => {
    const payload = { project_id: 7, template_id: 11, secret: '{"password":"hidden"}' };
    const plan = {
      fingerprint: 'sha256:reviewed', review_token: 'opaque-review', findings: [],
    };
    const calls = [];
    axios.post = async (url, body) => {
      calls.push({ url, body });
      return { data: plan };
    };
    const emitted = [];
    const context = {
      formError: null,
      formSaving: false,
      executionPreflight: null,
      executionPreflightPayloadSignature: null,
      deploymentWindowBlock: null,
      projectId: 7,
      $refs: { form: { validate: () => true } },
      $emit: (...args) => emitted.push(args),
      $t: (key) => key,
      beforeSave: async () => {},
      taskSavePayload: () => payload,
      taskStartPayload: TaskForm.methods.taskStartPayload,
      isExecutionPreflightUnavailable: TaskForm.methods.isExecutionPreflightUnavailable,
    };
    let submitted;
    context.submitTaskPayload = async (body, headers) => {
      submitted = { body, headers };
      return body;
    };

    await TaskForm.methods.save.call(context);

    expect(calls).to.deep.equal([{
      url: '/api/project/7/tasks/preflight', body: payload,
    }]);
    expect(context.executionPreflight).to.equal(plan);
    expect(emitted[0][0]).to.equal('preflight');

    await TaskForm.methods.save.call(context);

    expect(submitted.body).to.equal(payload);
    expect(submitted.headers['X-Semaphore-Preflight-Fingerprint']).to.equal('sha256:reviewed');
    const reviewHeader = Object.keys(submitted.headers).find((name) => name.endsWith('-Token'));
    expect(submitted.headers[reviewHeader]).to.equal('opaque-review');
  });

  it('clears the dialog save request after rendering a task preview', () => {
    let cleared = 0;
    const context = { preflightReady: false };

    NewTaskDialog.methods.handlePreflight.call(context, () => { cleared += 1; });

    expect(context.preflightReady).to.equal(true);
    expect(cleared).to.equal(1);
  });

  it('reviews a workflow and emits only the opaque review plus unchanged payload', async () => {
    const payload = { parameters: { region: 'eu' } };
    const plan = {
      fingerprint: 'sha256:workflow', review_token: 'workflow-review', findings: [],
    };
    axios.post = async () => ({ data: plan });
    const emitted = [];
    const context = {
      errors: [],
      hasDenial: false,
      executionPreflight: null,
      executionPreflightPayloadSignature: null,
      preflightLoading: false,
      preflightMessage: null,
      deploymentWindowBlock: null,
      projectId: 7,
      workflow: { id: 41 },
      buildPayload: () => payload,
      buildStartPayload: WorkflowRunDialog.methods.buildStartPayload,
      isExecutionPreflightUnavailable: WorkflowRunDialog.methods.isExecutionPreflightUnavailable,
      $emit: (...args) => emitted.push(args),
      $t: (key) => key,
    };

    await WorkflowRunDialog.methods.submit.call(context);
    await WorkflowRunDialog.methods.submit.call(context);

    expect(emitted).to.deep.equal([['start', {
      payload,
      review: { fingerprint: 'sha256:workflow', reviewToken: 'workflow-review' },
    }]]);
  });

  it('adopts a fresh stale-plan response and requires another confirmation', () => {
    const context = {
      executionPreflight: null,
      executionPreflightPayloadSignature: null,
      preflightMessage: null,
      buildPayload: () => ({}),
      $t: (key) => key,
    };
    const plan = { fingerprint: 'sha256:fresh', review_token: 'fresh-review' };

    WorkflowRunDialog.methods.adoptExecutionPreflight.call(context, plan, { parameters: {} });

    expect(context.executionPreflight).to.equal(plan);
    expect(context.preflightMessage).to.equal('executionPreflightChanged');
    expect(context.executionPreflightPayloadSignature).to.equal('{"parameters":{}}');
  });

  it('maps findings to compact visual severities', () => {
    expect(ExecutionPreflightReview.methods.findingType('denial')).to.equal('error');
    expect(ExecutionPreflightReview.methods.findingType('warning')).to.equal('warning');
    expect(ExecutionPreflightReview.methods.findingType('info')).to.equal('info');
  });

  it('shows bounded policy provenance and only safe remediation links', () => {
    const finding = {
      policy_scope: 'project',
      policy_revision: 4,
      policy_rule_id: 'deny-production',
      remediation_url: 'https://docs.example.test/policies/production',
    };
    expect(ExecutionPreflightReview.methods.policyFindingLabel(finding))
      .to.equal('project · deny-production · revision 4');
    expect(ExecutionPreflightReview.methods.safeRemediationURL(finding.remediation_url))
      .to.equal(finding.remediation_url);
    expect(ExecutionPreflightReview.methods.safeRemediationURL('data:text/html,unsafe'))
      .to.equal(null);
    expect(ExecutionPreflightReview.methods.safeRemediationURL('https://user:pass@example.test/'))
      .to.equal(null);
  });
});
