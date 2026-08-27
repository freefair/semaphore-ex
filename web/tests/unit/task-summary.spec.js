import { expect } from 'chai';
import AnsibleStageView from '@/components/EnhancedTaskSummary.vue';

function displayState(data = {}, featureAvailable = true) {
  return AnsibleStageView.computed.summaryDisplayState.call({
    featureAvailable,
    ...AnsibleStageView.data(),
    ...data,
  });
}

function loadContext(responses) {
  const data = AnsibleStageView.data();
  const context = {
    ...data,
    featureAvailable: true,
    taskId: 11,
    resetPagination: AnsibleStageView.methods.resetPagination,
    pageEndpoint: AnsibleStageView.methods.pageEndpoint,
    pageItemsProperty: AnsibleStageView.methods.pageItemsProperty,
    applyPage: AnsibleStageView.methods.applyPage,
    fetchPage: AnsibleStageView.methods.fetchPage,
    loadProjectEndpoint: async (endpoint) => responses[endpoint],
  };
  return context;
}

describe('persisted task summary', () => {
  it('models loading, failure, empty, unsupported, and partial states explicitly', () => {
    expect(displayState({ loading: true })).to.equal('loading');
    expect(displayState({ loadError: new Error('offline') })).to.equal('error');
    expect(displayState({ summary: { state: 'empty' } })).to.equal('empty');
    expect(displayState({ summary: { state: 'unsupported' } })).to.equal('unsupported');
    expect(displayState({ summary: { state: 'partial' } })).to.equal('partial');
  });

  it('does not synthesize results when the feature is unavailable', async () => {
    const context = {
      ...AnsibleStageView.data(),
      featureAvailable: false,
      loadProjectEndpoint: async () => {
        throw new Error('must not load persisted data');
      },
    };

    await AnsibleStageView.methods.loadData.call(context);

    expect(displayState({}, false)).to.equal('unavailable');
    expect(context.summary).to.equal(null);
    expect(AnsibleStageView.methods).not.to.have.property('getDemoData');
  });

  it('loads successful multi-host results only from persisted summary endpoints', async () => {
    const context = loadContext({
      '/tasks/11/ansible/summary': {
        state: 'complete', total_hosts: 3, ok_hosts: 2, failed_hosts: 1,
      },
      '/tasks/11/ansible/summary/hosts': {
        items: [
          { id: 3, host: 'web-03', status: 'success' },
          { id: 2, host: 'web-02', status: 'success' },
          { id: 1, host: 'web-01', status: 'failed' },
        ],
      },
      '/tasks/11/ansible/summary/stages': {
        items: [{
          id: 4, stage: 'Deploy', ok: 2, failed: 1,
        }],
      },
      '/tasks/11/ansible/summary/errors': {
        items: [{
          id: 5, event_id: 'failed-1', host: 'web-01', error: 'service failed',
        }],
      },
    });

    await AnsibleStageView.methods.loadData.call(context);

    expect(context.loadError).to.equal(null);
    expect(context.summary.state).to.equal('complete');
    expect(context.hostsPage.map((host) => host.host)).to.deep.equal(['web-03', 'web-02', 'web-01']);
    expect(context.errorsPage[0].error).to.equal('service failed');
  });

  it('replaces pages and supports previous navigation without growing browser memory', async () => {
    const context = {
      ...AnsibleStageView.data(),
      pageLoading: null,
      fetchPage: async (_kind, before) => ({
        items: before === 8 ? [{ id: 7, host: 'page-two' }] : [{ id: 9, host: 'page-one' }],
        next_cursor: before === 8 ? null : 8,
      }),
      pageItemsProperty: AnsibleStageView.methods.pageItemsProperty,
      applyPage: AnsibleStageView.methods.applyPage,
    };
    context.hostsPage = [{ id: 9, host: 'page-one' }];
    context.pagination.hosts.next = 8;

    await AnsibleStageView.methods.nextPage.call(context, 'hosts');
    expect(context.hostsPage.map((host) => host.host)).to.deep.equal(['page-two']);
    expect(context.pagination.hosts.history).to.deep.equal([null]);

    await AnsibleStageView.methods.previousPage.call(context, 'hosts');
    expect(context.hostsPage.map((host) => host.host)).to.deep.equal(['page-one']);
    expect(context.pagination.hosts.history).to.deep.equal([]);
  });
});
