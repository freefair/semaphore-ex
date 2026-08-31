import { expect } from 'chai';
import axios from 'axios';
import WorkflowVersionsDialog from '@/components/WorkflowVersionsDialog.vue';

describe('workflow version history UI', () => {
  let originalGet;
  let originalPost;

  beforeEach(() => {
    originalGet = axios.get;
    originalPost = axios.post;
  });

  afterEach(() => {
    axios.get = originalGet;
    axios.post = originalPost;
  });

  it('loads a bounded timeline and previews a restore through structural diff', async () => {
    const calls = [];
    axios.get = async (url) => {
      calls.push(url);
      if (url.includes('/diff')) {
        return { data: { changes: [{ section: 'metadata', before: {}, after: {} }] } };
      }
      return { data: [{ id: 2, version_number: 2 }, { id: 1, version_number: 1 }] };
    };
    const context = {
      projectId: 7,
      workflowId: 41,
      versions: [],
      loading: false,
      error: '',
      compareFrom: null,
      compareTo: null,
      comparison: null,
      restorePreview: null,
      restoreMessage: '',
      comparing: false,
      latestVersionNumber: 2,
      fetchDiff: WorkflowVersionsDialog.methods.fetchDiff,
      $t: (key) => key,
    };

    await WorkflowVersionsDialog.methods.loadVersions.call(context);
    await WorkflowVersionsDialog.methods.previewRestore.call(context, context.versions[1]);

    expect(context.versions.map((version) => version.version_number)).to.deep.equal([2, 1]);
    expect(context.restorePreview.version.version_number).to.equal(1);
    expect(context.restorePreview.diff.changes[0].section).to.equal('metadata');
    expect(calls[0]).to.include('count=50');
    expect(calls[1]).to.include('from=1&to=2');
  });

  it('restores by creating a new version and emits the returned definition', async () => {
    let request;
    const emitted = [];
    let reloaded = false;
    axios.post = async (url, body) => {
      request = { url, body };
      return { data: { id: 41, revision: 3, name: 'Deploy' } };
    };
    const context = {
      projectId: 7,
      workflowId: 41,
      restorePreview: { version: { version_number: 1 }, diff: { changes: [] } },
      restoreMessage: 'Restore initial',
      restoring: false,
      error: '',
      byteLength: WorkflowVersionsDialog.methods.byteLength,
      async loadVersions() { reloaded = true; },
      $emit(event, value) { emitted.push({ event, value }); },
    };

    await WorkflowVersionsDialog.methods.confirmRestore.call(context);

    expect(request.url).to.equal('/api/project/7/workflows/41/versions/1/restore');
    expect(request.body).to.deep.equal({ message: 'Restore initial' });
    expect(emitted).to.deep.equal([{
      event: 'restored', value: { id: 41, revision: 3, name: 'Deploy' },
    }]);
    expect(reloaded).to.equal(true);
  });
});
