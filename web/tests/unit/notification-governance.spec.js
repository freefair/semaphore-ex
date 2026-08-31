import { expect } from 'chai';
import axios from 'axios';
import './local-storage-fixture';
import NotificationGovernance from '@/components/NotificationGovernance.vue';

const response = (data) => ({
  data, status: 200, statusText: 'OK', headers: {}, config: {},
});

const translate = (key) => key;

const notificationError = (status, secret = 'provider-secret') => ({
  response: { status, data: { error: secret } },
});

describe('notification governance', () => {
  let originalAdapter;

  beforeEach(() => {
    originalAdapter = axios.defaults.adapter;
  });

  afterEach(() => {
    axios.defaults.adapter = originalAdapter;
  });

  it('keeps governance collapsed until its contained section is opened', async () => {
    let loadCalls = 0;
    const initial = NotificationGovernance.data.call({ $t: translate });
    const context = {
      expanded: initial.expanded,
      loaded: false,
      loading: false,
      load: async () => { loadCalls += 1; },
    };

    expect(context.expanded).to.equal(null);
    await NotificationGovernance.methods.onExpansion.call(context, 0);
    expect(loadCalls).to.equal(1);
  });

  it('loads destinations, rules, and paginated history from global endpoints only', async () => {
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config);
      if (config.url.endsWith('/destinations')) return response([{ id: 1, name: 'Primary' }]);
      if (config.url.endsWith('/rules')) return response([{ id: 2, destination_id: 1 }]);
      if (config.url.endsWith('/events')) return response([{ event_id: 'event-4', routing_outcome: 'filtered' }]);
      return response([{ id: 3, status: 'failed' }]);
    };
    const context = {
      loading: false,
      loaded: false,
      unavailable: false,
      error: '',
      destinations: [],
      rules: [],
      events: [],
      eventHistoryHasMore: false,
      eventHistoryLoading: false,
      deliveries: [],
      historyHasMore: false,
      historyLoading: false,
      handleError: NotificationGovernance.methods.handleError,
      loadHistory: NotificationGovernance.methods.loadHistory,
      loadEventHistory: NotificationGovernance.methods.loadEventHistory,
      $t: translate,
    };

    await NotificationGovernance.methods.load.call(context);

    expect(context.loaded).to.equal(true);
    expect(context.destinations).to.deep.equal([{ id: 1, name: 'Primary' }]);
    expect(context.rules).to.deep.equal([{ id: 2, destination_id: 1 }]);
    expect(context.events).to.deep.equal([{ event_id: 'event-4', routing_outcome: 'filtered' }]);
    expect(context.deliveries).to.deep.equal([{ id: 3, status: 'failed' }]);
    expect(requests.map((request) => request.url)).to.deep.equal([
      '/api/notification-governance/destinations',
      '/api/notification-governance/rules',
      '/api/notification-governance/deliveries',
      '/api/notification-governance/events',
    ]);
    expect(requests.every((request) => !request.url.includes('/project/'))).to.equal(true);
  });

  it('submits credentials once, preserves blank credentials on edit, and clears transient state', async () => {
    const payloads = [];
    const routingKey = 'a'.repeat(32);
    axios.defaults.adapter = async (config) => {
      payloads.push(JSON.parse(config.data));
      return response({
        id: 7,
        name: 'Primary',
        provider: 'pagerduty',
        environment: 'production',
        region: 'us',
        credential_configured: true,
        enabled: true,
        paused: false,
        revision: 1,
      });
    };
    const context = {
      $refs: { destinationForm: { validate: () => true } },
      $t: translate,
      destinationSaving: false,
      destinationDialog: true,
      destinationForm: {
        id: null,
        revision: 0,
        name: 'Primary',
        provider: 'pagerduty',
        environment: 'production',
        region: 'us',
        credential: routingKey,
        enabled: true,
      },
      destinations: [],
      error: '',
      upsertDestination: NotificationGovernance.methods.upsertDestination,
      closeDestination: NotificationGovernance.methods.closeDestination,
      handleError: NotificationGovernance.methods.handleError,
    };

    await NotificationGovernance.methods.saveDestination.call(context);

    expect(payloads).to.deep.equal([{
      name: 'Primary', provider: 'pagerduty', environment: 'production', region: 'us', enabled: true, credential: routingKey,
    }]);
    expect(context.destinationForm.credential).to.equal('');
    expect(JSON.stringify(context.destinations)).not.to.include(routingKey);
    expect(context.destinationDialog).to.equal(false);

    NotificationGovernance.methods.openDestination.call(context, {
      id: 7,
      revision: 1,
      name: 'Primary',
      provider: 'pagerduty',
      environment: 'production',
      region: 'eu',
      enabled: true,
      credential: 'must-not-render',
    });
    expect(context.destinationForm.credential).to.equal('');
    expect(context.destinationForm.region).to.equal('eu');
    await NotificationGovernance.methods.saveDestination.call(context);
    expect(payloads[1]).to.deep.equal({
      name: 'Primary',
      provider: 'pagerduty',
      environment: 'production',
      region: 'eu',
      enabled: true,
      revision: 1,
    });
  });

  it('does not submit a destination while an earlier save is in progress', async () => {
    let requests = 0;
    axios.defaults.adapter = async () => {
      requests += 1;
      return response({});
    };
    const context = {
      destinationSaving: true,
      destinationForm: {
        id: null,
        revision: 0,
        name: '',
        provider: '',
        environment: '',
        region: 'us',
        credential: '',
        enabled: true,
      },
      $refs: { destinationForm: { validate: () => true } },
    };

    await NotificationGovernance.methods.saveDestination.call(context);

    expect(requests).to.equal(0);
  });

  it('saves a rule with explicit filters and previews the selected typed event', async () => {
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push({ url: config.url, data: config.data ? JSON.parse(config.data) : undefined });
      if (config.url.endsWith('/routing/preview')) {
        return response([{
          destination_id: 4, name: 'Primary', provider: 'generic', environment: 'production',
        }]);
      }
      return response({
        id: 5, destination_id: 4, source_kinds: ['system'], lifecycle_actions: ['trigger'], minimum_severity: 'info', enabled: true, revision: 1,
      });
    };
    const context = {
      $refs: { ruleForm: { validate: () => true } },
      $t: translate,
      ruleSaving: false,
      ruleDialog: true,
      ruleForm: {
        id: null, revision: 0, destination_id: 4, source_kinds: ['system'], lifecycle_actions: ['trigger'], minimum_severity: 'info', enabled: true,
      },
      rules: [],
      previewing: false,
      preview: [],
      previewed: false,
      error: '',
      previewSourceKind: 'system',
      previewAction: 'trigger',
      previewSeverity: 'info',
      upsertRule: NotificationGovernance.methods.upsertRule,
      closeRule: NotificationGovernance.methods.closeRule,
      handleError: NotificationGovernance.methods.handleError,
      previewEvent: NotificationGovernance.methods.previewEvent,
    };

    await NotificationGovernance.methods.saveRule.call(context);
    await NotificationGovernance.methods.previewRouting.call(context);

    expect(requests[0]).to.deep.equal({
      url: '/api/notification-governance/rules',
      data: {
        destination_id: 4, source_kinds: ['system'], lifecycle_actions: ['trigger'], minimum_severity: 'info', enabled: true,
      },
    });
    expect(requests[1]).to.deep.equal({
      url: '/api/notification-governance/routing/preview',
      data: {
        schema_version: 'semaphore.notification.v1',
        source_revision: 1,
        scope: 'global',
        source: { kind: 'system', id: 'system:notification-preview' },
        lifecycle_id: 'system:notification-preview',
        severity: 'info',
        lifecycle_action: 'trigger',
        details: {},
      },
    });
    expect(context.preview).to.have.length(1);
  });

  it('builds a typed task resolve event from the selected preview filters', () => {
    const event = NotificationGovernance.methods.previewEvent.call({
      previewSourceKind: 'task',
      previewAction: 'resolve',
      previewSeverity: 'error',
    });

    expect(event).to.deep.include({
      severity: 'error',
      lifecycle_action: 'resolve',
      source: { kind: 'task', id: 'task:9001' },
      lifecycle_id: 'template:9002',
      details: { task_id: 9001, template_id: 9002, status: 'succeeded' },
    });
  });

  it('queues test, pause, retry, and history pagination without duplicate work', async () => {
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config.url);
      if (config.url.endsWith('/retry')) return response({ id: 9, status: 'pending' });
      if (config.url.endsWith('/pause')) {
        return response({
          id: 4, paused: true, enabled: true, credential_configured: true, revision: 2,
        });
      }
      if (config.url.endsWith('/deliveries')) return response(Array.from({ length: 25 }, (_, index) => ({ id: index + 10, status: 'failed' })));
      return response({ id: 9, status: 'pending' });
    };
    let historyLoads = 0;
    const context = {
      $t: translate,
      error: '',
      testingDestinationId: null,
      pausingDestinationId: null,
      retryingDeliveryId: null,
      destinations: [{
        id: 4, enabled: true, paused: false, credential_configured: true, revision: 1,
      }],
      deliveries: [{ id: 9, status: 'failed' }],
      unavailable: false,
      historyLoading: false,
      historyHasMore: false,
      canTest: NotificationGovernance.methods.canTest,
      handleError: NotificationGovernance.methods.handleError,
      upsertDestination: NotificationGovernance.methods.upsertDestination,
      upsertDelivery: NotificationGovernance.methods.upsertDelivery,
      loadHistory: async () => { historyLoads += 1; },
    };

    await NotificationGovernance.methods.testDestination.call(context, context.destinations[0]);
    await NotificationGovernance.methods.setDestinationPaused.call(
      context,
      context.destinations[0],
      true,
    );
    await NotificationGovernance.methods.retryDelivery.call(context, { id: 9, status: 'failed' });
    expect(context.deliveries[0].status).to.equal('pending');
    context.loadHistory = NotificationGovernance.methods.loadHistory;
    await NotificationGovernance.methods.loadHistory.call(context, true);

    expect(historyLoads).to.equal(1);
    expect(context.destinations[0].paused).to.equal(true);
    expect(context.historyHasMore).to.equal(true);
    expect(requests).to.deep.equal([
      '/api/notification-governance/destinations/4/test',
      '/api/notification-governance/destinations/4/pause',
      '/api/notification-governance/deliveries/9/retry',
      '/api/notification-governance/deliveries',
    ]);
  });

  it('shows safe conflict and unavailable states without rendering response bodies', () => {
    const context = { $t: translate, unavailable: false, error: '' };

    NotificationGovernance.methods.handleError.call(context, notificationError(409));
    expect(context.error).to.equal('notificationConflict');
    expect(context.error).not.to.include('provider-secret');

    NotificationGovernance.methods.handleError.call(context, notificationError(503));
    expect(context.unavailable).to.equal(true);
    expect(context.error).to.equal('');
  });

  it('formats only allow-listed history values and never falls back to raw reason text', () => {
    const context = { $t: translate };
    const source = NotificationGovernance.methods.historySourceLabel.call(context, {
      source_kind: 'task', source_id: 'task:42',
    });
    const lifecycle = NotificationGovernance.methods.historyLifecycleLabel.call(context, {
      lifecycle_action: 'trigger', severity: 'error',
    });
    const reason = NotificationGovernance.methods.deliveryReasonLabel.call(context, 'transport_error');
    const unknownReason = NotificationGovernance.methods.deliveryReasonLabel.call(context, 'provider response token=secret');

    expect(source).to.equal('notificationSourceTask · task:42');
    expect(lifecycle).to.equal('notificationActionTrigger · notificationSeverityError');
    expect(reason).to.equal('notificationReasonTransport');
    expect(unknownReason).to.equal('—');
    expect(NotificationGovernance.methods.routingOutcomeLabel.call(context, 'filtered')).to.equal('notificationRoutingOutcomeFiltered');
    expect(NotificationGovernance.methods.providerRegionLabel.call(context, 'us')).to.equal('notificationRegionUS');
    expect(NotificationGovernance.methods.providerRegionLabel.call(context, 'eu')).to.equal('notificationRegionEU');
    expect(NotificationGovernance.methods.providerRegionLabel.call(context, 'custom')).to.equal('—');
  });

  it('requires an exact PagerDuty routing key only when a new key is entered', () => {
    const compute = NotificationGovernance.computed.destinationCredentialRules;
    const blank = compute.call({
      destinationForm: { provider: 'pagerduty', credential: '' },
      $t: translate,
    });
    const rules = compute.call({
      destinationForm: { provider: 'pagerduty', credential: 'short' },
      $t: translate,
    });
    const unrelated = compute.call({
      destinationForm: { provider: 'generic', credential: 'short' },
      $t: translate,
    });

    expect(blank).to.deep.equal([]);
    expect(unrelated).to.deep.equal([]);
    expect(rules[0]('a'.repeat(32))).to.equal(true);
    expect(rules[0]('short')).to.equal('notificationPagerDutyKeyLength');
  });

  it('keeps delivery and routing-event pagination offsets independent', async () => {
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push({ url: config.url, offset: config.params.offset });
      return response([]);
    };
    const context = {
      unavailable: false,
      historyLoading: false,
      deliveries: [{ id: 1 }, { id: 2 }],
      historyHasMore: true,
      eventHistoryLoading: false,
      events: [{ event_id: 'event-1' }],
      eventHistoryHasMore: true,
      handleError: NotificationGovernance.methods.handleError,
      $t: translate,
    };

    await NotificationGovernance.methods.loadHistory.call(context, false);
    await NotificationGovernance.methods.loadEventHistory.call(context, false);

    expect(requests).to.deep.equal([
      { url: '/api/notification-governance/deliveries', offset: 2 },
      { url: '/api/notification-governance/events', offset: 1 },
    ]);
  });
});
