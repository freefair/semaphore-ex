<template>
  <div data-testid="audit-webhook-page">
    <v-toolbar flat>
      <v-btn icon class="mr-4" @click="returnToProjects()" aria-label="Back to projects">
        <v-icon>mdi-arrow-left</v-icon>
      </v-btn>
      <v-toolbar-title>{{ $t('auditWebhook') }}</v-toolbar-title>
      <v-spacer />
      <v-chip
        v-if="configured"
        small
        :color="config.paused ? 'warning' : 'success'"
        dark
        data-testid="audit-webhook-state"
      >
        {{ config.paused ? $t('paused') : $t('active') }}
      </v-chip>
    </v-toolbar>
    <v-divider />

    <v-alert v-if="unavailable" text type="info" class="PageAlert" data-testid="unavailable">
      {{ $t('auditWebhookUnavailable') }}
    </v-alert>

    <div class="pa-4 audit-webhook-content">
      <v-alert v-if="error" text type="error" dismissible @input="error = ''">
        {{ error }}
      </v-alert>

      <template v-if="!unavailable">
      <v-card outlined class="mb-4">
        <v-card-title class="subtitle-1">
          <v-icon left>mdi-webhook</v-icon>
          {{ $t('auditWebhookConfiguration') }}
        </v-card-title>
        <v-card-text>
          <p class="text-body-2 mb-5">
            {{ $t('auditWebhookDescription') }}
          </p>
          <v-form ref="form" v-model="valid" @submit.prevent="saveConfiguration()">
            <v-text-field
              v-model.trim="endpoint"
              :label="$t('auditWebhookEndpoint')"
              placeholder="https://audit.example.com/v1/events"
              prepend-inner-icon="mdi-lock"
              :rules="endpointRules"
              :disabled="loading || saving"
              outlined
              dense
              data-testid="audit-webhook-endpoint"
            />
            <v-text-field
              v-model="credential"
              :label="$t('auditWebhookCredential')"
              type="password"
              autocomplete="new-password"
              :hint="credentialHint"
              persistent-hint
              :disabled="loading || saving || removeCredential"
              outlined
              dense
              data-testid="audit-webhook-credential"
            />
            <v-checkbox
              v-if="config.credential_configured"
              v-model="removeCredential"
              :label="$t('auditWebhookRemoveCredential')"
              :disabled="loading || saving"
              dense
              data-testid="audit-webhook-remove-credential"
            />
          </v-form>
        </v-card-text>
        <v-card-actions class="px-4 pb-4 audit-webhook-actions">
          <v-btn
            class="audit-webhook-action"
            color="primary"
            :loading="saving"
            :disabled="loading || !valid"
            @click="saveConfiguration()"
            data-testid="audit-webhook-save"
          >
            <v-icon left>mdi-content-save</v-icon>
            {{ $t('save') }}
          </v-btn>
          <v-btn
            class="audit-webhook-action"
            outlined
            :loading="testing"
            :disabled="!configured || saving || pausing"
            @click="sendTestDelivery()"
            data-testid="audit-webhook-test"
          >
            <v-icon left>mdi-send-check</v-icon>
            {{ $t('auditWebhookSendTest') }}
          </v-btn>
          <v-spacer class="audit-webhook-spacer" />
          <v-btn
            v-if="configured"
            class="audit-webhook-action"
            text
            :color="config.paused ? 'success' : 'warning'"
            :loading="pausing"
            :disabled="saving || testing"
            @click="setPaused(!config.paused)"
            data-testid="audit-webhook-pause-toggle"
          >
            <v-icon left>{{ config.paused ? 'mdi-play' : 'mdi-pause' }}</v-icon>
            {{ config.paused ? $t('resume') : $t('pause') }}
          </v-btn>
        </v-card-actions>
      </v-card>

      <v-card outlined>
        <v-card-title class="subtitle-1">
          <v-icon left>mdi-history</v-icon>
          {{ $t('auditWebhookDeliveryHistory') }}
          <v-spacer />
          <v-btn
            icon
            :loading="historyLoading"
            @click="loadHistory(true)"
            :aria-label="$t('refresh')"
          >
            <v-icon>mdi-refresh</v-icon>
          </v-btn>
        </v-card-title>
        <v-data-table
          :headers="headers"
          :items="deliveries"
          :loading="historyLoading && deliveries.length === 0"
          :hide-default-footer="true"
          :mobile-breakpoint="720"
          item-key="id"
          data-testid="audit-webhook-history"
        >
          <template v-slot:item.event_id="{ item }">
            <code class="event-id">{{ item.event_id }}</code>
          </template>
          <template v-slot:item.status="{ item }">
            <v-chip x-small dark :color="statusColor(item.status)" :data-status="item.status">
              {{ statusLabel(item.status) }}
            </v-chip>
          </template>
          <template v-slot:item.http_status="{ item }">
            {{ item.http_status || '—' }}
          </template>
          <template v-slot:item.last_error="{ item }">
            {{ errorLabel(item.last_error) }}
          </template>
          <template v-slot:item.updated_at="{ item }">
            {{ formatTimestamp(item.updated_at) }}
          </template>
          <template v-slot:no-data>
            <div class="py-8 grey--text" data-testid="audit-webhook-empty">
              {{ $t('auditWebhookNoDeliveries') }}
            </div>
          </template>
        </v-data-table>
        <v-card-actions v-if="hasMore" class="justify-center">
          <v-btn text :loading="historyLoading" @click="loadHistory(false)">
            {{ $t('loadMore') }}
          </v-btn>
        </v-card-actions>
      </v-card>
      </template>

      <NotificationGovernance />
    </div>
  </div>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import NotificationGovernance from '@/components/NotificationGovernance.vue';

const PAGE_SIZE = 25;

export default {
  name: 'AuditWebhooks',

  components: {
    NotificationGovernance,
  },

  data() {
    return {
      loading: true,
      historyLoading: false,
      saving: false,
      testing: false,
      pausing: false,
      unavailable: false,
      error: '',
      valid: false,
      endpoint: '',
      credential: '',
      removeCredential: false,
      config: {
        endpoint: '',
        credential_configured: false,
        paused: false,
      },
      deliveries: [],
      hasMore: false,
      endpointRules: [
        (value) => !!value || this.$t('auditWebhookEndpointRequired'),
        (value) => /^https:\/\/[^\s]+$/i.test(value || '') || this.$t('auditWebhookHttpsRequired'),
      ],
    };
  },

  computed: {
    configured() {
      return !!this.config.endpoint;
    },

    credentialHint() {
      return this.config.credential_configured
        ? this.$t('auditWebhookCredentialPreserved')
        : this.$t('auditWebhookCredentialOptional');
    },

    headers() {
      return [
        { text: this.$t('auditWebhookEventId'), value: 'event_id', sortable: false },
        { text: this.$t('status'), value: 'status', sortable: false },
        { text: this.$t('attempts'), value: 'attempts', sortable: false },
        { text: 'HTTP', value: 'http_status', sortable: false },
        { text: this.$t('lastError'), value: 'last_error', sortable: false },
        { text: this.$t('updated'), value: 'updated_at', sortable: false },
      ];
    },
  },

  async created() {
    await this.load();
  },

  methods: {
    async returnToProjects() {
      EventBus.$emit('i-open-last-project');
    },

    async load() {
      this.loading = true;
      this.error = '';
      try {
        const response = await axios.get('/api/audit-webhook');
        this.applyConfiguration(response.data);
        await this.loadHistory(true);
      } catch (err) {
        if (err.response && err.response.status === 404) {
          this.unavailable = true;
        } else {
          this.error = getErrorMessage(err);
        }
      } finally {
        this.loading = false;
      }
    },

    applyConfiguration(config) {
      this.config = {
        endpoint: config.endpoint || '',
        credential_configured: !!config.credential_configured,
        paused: !!config.paused,
      };
      this.endpoint = this.config.endpoint;
      this.credential = '';
      this.removeCredential = false;
    },

    async saveConfiguration() {
      if (this.$refs.form && !this.$refs.form.validate()) {
        return;
      }
      this.saving = true;
      this.error = '';
      try {
        const payload = { endpoint: this.endpoint.trim() };
        if (this.removeCredential) {
          payload.credential = '';
        } else if (this.credential !== '') {
          payload.credential = this.credential;
        }
        const response = await axios.put('/api/audit-webhook', payload);
        this.applyConfiguration(response.data);
        EventBus.$emit('i-snackbar', { color: 'success', text: this.$t('auditWebhookSaved') });
        await this.loadHistory(true);
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.credential = '';
        this.saving = false;
      }
    },

    async sendTestDelivery() {
      this.testing = true;
      this.error = '';
      try {
        const response = await axios.post('/api/audit-webhook/test');
        EventBus.$emit('i-snackbar', {
          color: response.data.status === 'succeeded' ? 'success' : 'warning',
          text: this.$t('auditWebhookTestQueued'),
        });
        await this.loadHistory(true);
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.testing = false;
      }
    },

    async setPaused(paused) {
      this.pausing = true;
      this.error = '';
      try {
        const action = paused ? 'pause' : 'resume';
        const response = await axios.post(`/api/audit-webhook/${action}`);
        this.applyConfiguration(response.data);
        EventBus.$emit('i-snackbar', {
          color: paused ? 'warning' : 'success',
          text: paused ? this.$t('auditWebhookPaused') : this.$t('auditWebhookResumed'),
        });
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.pausing = false;
      }
    },

    async loadHistory(reset) {
      if (this.unavailable) return;
      this.historyLoading = true;
      try {
        const offset = reset ? 0 : this.deliveries.length;
        const response = await axios.get('/api/audit-webhook/deliveries', {
          params: { count: PAGE_SIZE, offset },
        });
        this.deliveries = reset ? response.data : this.deliveries.concat(response.data);
        this.hasMore = response.data.length === PAGE_SIZE;
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.historyLoading = false;
      }
    },

    statusColor(status) {
      return {
        pending: 'grey',
        delivering: 'blue',
        retrying: 'warning',
        succeeded: 'success',
        failed: 'error',
      }[status] || 'grey';
    },

    statusLabel(status) {
      return this.$t(`auditWebhookStatus_${status}`);
    },

    errorLabel(reason) {
      if (!reason) return '—';
      const known = [
        'timeout', 'network_error', 'response_too_large', 'client_response',
        'server_response', 'redirect_response', 'attempts_exhausted', 'configuration_error',
      ];
      return known.includes(reason) ? this.$t(`auditWebhookError_${reason}`) : this.$t('error');
    },

    formatTimestamp(value) {
      if (!value || value.startsWith('0001-01-01')) return '—';
      return new Date(value).toLocaleString();
    },
  },
};
</script>

<style scoped>
.audit-webhook-content {
  max-width: 1280px;
  margin: 0 auto;
}

.event-id {
  white-space: nowrap;
  font-size: 0.78rem;
}

@media (max-width: 720px) {
  .audit-webhook-actions {
    align-items: stretch;
    flex-direction: column;
  }

  .audit-webhook-action {
    width: 100%;
    margin: 0 0 8px !important;
  }

  .audit-webhook-action:last-child {
    margin-bottom: 0 !important;
  }

  .audit-webhook-spacer {
    display: none;
  }

  .event-id {
    display: inline-block;
    max-width: 190px;
    overflow-wrap: anywhere;
    text-align: right;
    white-space: normal;
  }
}
</style>
