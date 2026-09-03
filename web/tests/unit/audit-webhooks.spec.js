import { expect } from 'chai';
import axios from 'axios';
import AuditWebhooks from '@/views/AuditWebhooks.vue';

describe('audit webhook administration', () => {
  it('maps setup, retrying, success, and permanent failure to explicit states', () => {
    const color = AuditWebhooks.methods.statusColor;
    expect(color('pending')).to.equal('grey');
    expect(color('retrying')).to.equal('warning');
    expect(color('succeeded')).to.equal('success');
    expect(color('failed')).to.equal('error');

    const $t = (key) => key;
    expect(AuditWebhooks.methods.statusLabel.call({ $t }, 'retrying'))
      .to.equal('auditWebhookStatus_retrying');
    expect(AuditWebhooks.methods.statusLabel.call({ $t }, 'failed'))
      .to.equal('auditWebhookStatus_failed');
    expect(AuditWebhooks.methods.errorLabel.call({ $t }, 'attempts_exhausted'))
      .to.equal('auditWebhookError_attempts_exhausted');
  });

  it('loads configuration and paginated history without creating secret state', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config);
      return {
        data: config.url.endsWith('/deliveries')
          ? [{
            id: 1,
            event_id: 'event-1',
            status: 'retrying',
            attempts: 2,
          }]
          : {
            endpoint: 'https://audit.example.test/events',
            credential_configured: true,
            paused: false,
          },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        loading: true,
        historyLoading: false,
        unavailable: false,
        error: '',
        config: {},
        endpoint: '',
        credential: '',
        removeCredential: false,
        deliveries: [],
        hasMore: false,
        applyConfiguration: AuditWebhooks.methods.applyConfiguration,
        loadHistory: AuditWebhooks.methods.loadHistory,
        dismissSigningSecret: AuditWebhooks.methods.dismissSigningSecret,
        signingSecret: '',
        signingSecretAcknowledged: false,
      };
      await AuditWebhooks.methods.load.call(context);

      expect(context.endpoint).to.equal('https://audit.example.test/events');
      expect(context.config.credential_configured).to.equal(true);
      expect(context.credential).to.equal('');
      expect(context.deliveries).to.deep.equal([
        {
          id: 1,
          event_id: 'event-1',
          status: 'retrying',
          attempts: 2,
        },
      ]);
      expect(JSON.stringify(context)).not.to.include('write-only-secret');
      expect(requests.map((request) => request.url)).to.deep.equal([
        '/api/audit-webhook',
        '/api/audit-webhook/deliveries',
      ]);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('sends a credential once and clears it immediately after saving', async () => {
    const previousAdapter = axios.defaults.adapter;
    let savedPayload;
    axios.defaults.adapter = async (config) => {
      if (config.method === 'put') {
        savedPayload = JSON.parse(config.data);
      }
      return {
        data: config.url.endsWith('/deliveries') ? [] : {
          endpoint: 'https://audit.example.test/events',
          credential_configured: true,
          paused: false,
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        $refs: { form: { validate: () => true } },
        $t: (key) => key,
        endpoint: 'https://audit.example.test/events',
        credential: 'write-only-secret',
        removeCredential: false,
        saving: false,
        error: '',
        config: {},
        deliveries: [],
        hasMore: false,
        historyLoading: false,
        applyConfiguration: AuditWebhooks.methods.applyConfiguration,
        loadHistory: AuditWebhooks.methods.loadHistory,
      };
      await AuditWebhooks.methods.saveConfiguration.call(context);

      expect(savedPayload).to.deep.equal({
        endpoint: 'https://audit.example.test/events',
        credential: 'write-only-secret',
      });
      expect(context.credential).to.equal('');
      expect(context.config).not.to.have.property('credential');
      expect(context.config.credential_configured).to.equal(true);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('keeps generated signing secrets transient and sends revision-fenced lifecycle calls', async () => {
    const previousAdapter = axios.defaults.adapter;
    const requests = [];
    axios.defaults.adapter = async (config) => {
      requests.push(config);
      return {
        data: {
          secret: 'swhsec_once',
          current_key_id: 'swhkid_current',
          current_generation: 1,
          signing_revision: 4,
        },
        status: 201,
        statusText: 'Created',
        headers: {},
        config,
      };
    };

    try {
      const context = {
        config: { signing_revision: 3 },
        signingMutating: false,
        signingSecret: '',
        signingSecretAcknowledged: true,
        error: '',
        applySigningStatus: AuditWebhooks.methods.applySigningStatus,
        revealSigningSecret: AuditWebhooks.methods.revealSigningSecret,
      };
      const mutate = AuditWebhooks.methods.mutateSigningSecret;
      await mutate.call(context, 'post', '/api/audit-webhook/signing-secret', true);

      expect(requests).to.have.length(1);
      expect(requests[0].url).to.equal('/api/audit-webhook/signing-secret');
      expect(requests[0].params).to.deep.equal({ revision: 3 });
      expect(context.signingSecret).to.equal('swhsec_once');
      expect(context.signingSecretAcknowledged).to.equal(false);
      expect(context.config.current_key_id).to.equal('swhkid_current');
      expect(JSON.stringify(context.config)).not.to.include('swhsec_once');

      AuditWebhooks.methods.dismissSigningSecret.call(context);
      expect(context.signingSecret).to.equal('');
      expect(context.signingSecretAcknowledged).to.equal(false);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('loads bounded redacted attempt history for one delivery', async () => {
    const previousAdapter = axios.defaults.adapter;
    let request;
    axios.defaults.adapter = async (config) => {
      request = config;
      return {
        data: [{
          id: 8, attempt: 2, key_id: 'swhkid_current', outcome: 'succeeded',
        }],
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };

    try {
      const delivery = { id: 12, event_id: 'evt_1234567890123456' };
      const context = {
        attemptDelivery: null,
        attempts: [],
        attemptDialog: false,
        attemptLoading: false,
        error: '',
      };
      await AuditWebhooks.methods.loadAttempts.call(context, delivery);

      expect(request.url).to.equal('/api/audit-webhook/deliveries/12/attempts');
      expect(request.params).to.deep.equal({ count: 100 });
      expect(context.attemptDialog).to.equal(true);
      expect(context.attempts[0]).to.deep.equal({
        id: 8, attempt: 2, key_id: 'swhkid_current', outcome: 'succeeded',
      });
      expect(JSON.stringify(context.attempts)).not.to.include('signature');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });
});
