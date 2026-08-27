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
});
