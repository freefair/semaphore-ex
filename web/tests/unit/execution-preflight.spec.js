import { expect } from 'chai';
import axios from 'axios';
import NewTaskDialog from '@/components/NewTaskDialog.vue';
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

  it('clears the dialog save request after rendering a task preview', () => {
    let cleared = 0;
    const context = {};

    NewTaskDialog.methods.handlePreflight.call(context, () => { cleared += 1; });

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
