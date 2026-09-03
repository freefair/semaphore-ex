<template>
  <div data-testid="audit-webhook-page">
    <v-toolbar flat>
      <v-btn icon class="mr-4" @click="returnToProjects()" aria-label="Back to projects">
        <v-icon>mdi-arrow-left</v-icon>
      </v-btn>
      <v-toolbar-title>
        {{ showAuditGovernance ? $t('auditWebhook') : $t('policyGuardrails') }}
      </v-toolbar-title>
      <v-spacer />
      <v-chip
        v-if="showAuditGovernance && configured"
        small
        :color="config.paused ? 'warning' : 'success'"
        dark
        data-testid="audit-webhook-state"
      >
        {{ config.paused ? $t('paused') : $t('active') }}
      </v-chip>
    </v-toolbar>
    <v-divider />

    <v-alert
      v-if="showAuditGovernance && unavailable"
      text
      type="info"
      class="PageAlert"
      data-testid="unavailable"
    >
      {{ $t('auditWebhookUnavailable') }}
    </v-alert>

    <div class="pa-4 audit-webhook-content">
      <v-alert
        v-if="showAuditGovernance && error"
        text
        type="error"
        dismissible
        @input="error = ''"
      >
        {{ error }}
      </v-alert>

      <v-alert
        v-if="showAuditGovernance && signingSecret"
        text
        type="warning"
        data-testid="audit-webhook-signing-secret"
      >
        <div class="font-weight-medium">{{ $t('webhookSigningSecretOnce') }}</div>
        <v-text-field
          :value="signingSecret"
          readonly
          hide-details
          class="mt-2"
          data-testid="audit-webhook-signing-secret-value"
          @focus="$event.target.select()"
        />
        <v-checkbox
          v-model="signingSecretAcknowledged"
          :label="$t('webhookSigningSecretStored')"
          hide-details
          data-testid="audit-webhook-signing-secret-acknowledge"
        />
        <v-btn
          text
          small
          class="mt-2"
          :disabled="!signingSecretAcknowledged"
          data-testid="audit-webhook-signing-secret-dismiss"
          @click="dismissSigningSecret()"
        >
          {{ $t('dismiss') }}
        </v-btn>
      </v-alert>

      <template v-if="showAuditGovernance && !unavailable">
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
          <v-divider class="my-5" />
          <div class="d-flex flex-wrap align-center mb-2 audit-webhook-signing-status">
            <div>
              <div class="text-subtitle-2">{{ $t('webhookSigning') }}</div>
              <div class="text-caption" data-testid="audit-webhook-current-key">
                {{ $t('webhookSigningCurrentKey') }}:
                <code>{{ config.current_key_id || '—' }}</code>
              </div>
              <div
                v-if="config.next_key_id"
                class="text-caption"
                data-testid="audit-webhook-next-key"
              >
                {{ nextSigningKeyStaged
                  ? $t('webhookSigningStagedKey') : $t('webhookSigningRetiredKey') }}:
                <code>{{ config.next_key_id }}</code>
              </div>
            </div>
            <v-spacer />
            <v-chip small :color="hasCurrentSigningKey ? 'success' : 'warning'" dark>
              {{ hasCurrentSigningKey
                ? $t('webhookSigningActive') : $t('webhookSigningSetupRequired') }}
            </v-chip>
          </div>
          <div class="d-flex flex-wrap audit-webhook-signing-actions">
            <v-btn
              v-if="!hasCurrentSigningKey"
              small
              outlined
              color="primary"
              :loading="signingMutating"
              data-testid="audit-webhook-signing-create"
              @click="createSigningSecret()"
            >{{ $t('webhookSigningCreate') }}</v-btn>
            <v-btn
              v-if="hasCurrentSigningKey && !hasNextSigningKey"
              small
              outlined
              :loading="signingMutating"
              data-testid="audit-webhook-signing-stage"
              @click="stageSigningSecret()"
            >{{ $t('webhookSigningStage') }}</v-btn>
            <v-btn
              v-if="nextSigningKeyStaged"
              small
              outlined
              color="primary"
              :loading="signingMutating"
              :disabled="!!signingSecret && !signingSecretAcknowledged"
              data-testid="audit-webhook-signing-promote"
              @click="promoteSigningSecret()"
            >{{ $t('webhookSigningPromote') }}</v-btn>
            <v-btn
              v-if="hasNextSigningKey"
              small
              outlined
              :loading="testing"
              data-testid="audit-webhook-test-next"
              @click="sendTestDelivery('next')"
            >{{ $t('webhookSigningTestNext') }}</v-btn>
            <v-btn
              v-if="hasNextSigningKey"
              small
              text
              color="error"
              :loading="signingMutating"
              data-testid="audit-webhook-signing-revoke"
              @click="revokeSigningSecret()"
            >{{ $t('webhookSigningRevokeNext') }}</v-btn>
          </div>
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
            :disabled="!configured || !hasCurrentSigningKey || saving || pausing"
            @click="sendTestDelivery('current')"
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
          <template v-slot:item.actions="{ item }">
            <v-btn
              icon
              small
              :aria-label="$t('webhookDeliveryAttempts')"
              data-testid="audit-webhook-attempts"
              @click="loadAttempts(item)"
            >
              <v-icon small>mdi-format-list-bulleted</v-icon>
            </v-btn>
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

      <v-dialog v-model="attemptDialog" max-width="900" scrollable>
        <v-card data-testid="audit-webhook-attempt-history">
          <v-card-title>{{ $t('webhookDeliveryAttempts') }}</v-card-title>
          <v-card-text>
            <div v-if="attemptDelivery" class="text-caption mb-3">
              {{ $t('auditWebhookEventId') }}: <code>{{ attemptDelivery.event_id }}</code>
            </div>
            <v-data-table
              :headers="attemptHeaders"
              :items="attempts"
              :loading="attemptLoading"
              :hide-default-footer="true"
              item-key="id"
            >
              <template v-slot:item.outcome="{ item }">
                <v-chip x-small dark :color="statusColor(item.outcome)">
                  {{ statusLabel(item.outcome) }}
                </v-chip>
              </template>
              <template v-slot:item.http_status="{ item }">
                {{ item.http_status || '—' }}
              </template>
              <template v-slot:item.reason="{ item }">
                {{ errorLabel(item.reason) }}
              </template>
              <template v-slot:item.signed_at="{ item }">
                {{ formatTimestamp(item.signed_at) }}
              </template>
            </v-data-table>
          </v-card-text>
          <v-card-actions>
            <v-spacer />
            <v-btn text @click="attemptDialog = false">{{ $t('close') }}</v-btn>
          </v-card-actions>
        </v-card>
      </v-dialog>
      </template>

      <NotificationGovernance v-if="showAuditGovernance" />

      <PolicyGuardrailsPanel
        v-if="policyGuardrailsDecision
          && (canManagePolicyGuardrails || canRollbackPolicyGuardrails)"
        :capability="policyGuardrailsDecision"
        :can-manage="canManagePolicyGuardrails"
        :can-rollback="canRollbackPolicyGuardrails"
      />
    </div>
  </div>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import NotificationGovernance from '@/components/NotificationGovernance.vue';
import PolicyGuardrailsPanel from '@/components/PolicyGuardrailsPanel.vue';
import { findCapabilityDecision } from '@/lib/capabilities';
import { GLOBAL_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

const PAGE_SIZE = 25;

export default {
  name: 'AuditWebhooks',

  components: {
    NotificationGovernance, PolicyGuardrailsPanel,
  },

  props: {
    systemInfo: { type: Object, default: () => ({}) },
    isAdmin: Boolean,
  },

  data() {
    return {
      loading: true,
      historyLoading: false,
      saving: false,
      testing: false,
      pausing: false,
      signingMutating: false,
      signingSecret: '',
      signingSecretAcknowledged: false,
      attemptDialog: false,
      attemptLoading: false,
      attemptDelivery: null,
      attempts: [],
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
        current_key_id: '',
        next_key_id: '',
        current_generation: 0,
        next_generation: 0,
        signing_revision: 0,
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
    showAuditGovernance() {
      const permission = GLOBAL_PERMISSIONS.manageSystem;
      return hasGlobalPermission(this.systemInfo, permission, this.isAdmin);
    },

    canManagePolicyGuardrails() {
      const permission = GLOBAL_PERMISSIONS.managePolicyGuardrails;
      return hasGlobalPermission(this.systemInfo, permission, this.isAdmin);
    },

    canRollbackPolicyGuardrails() {
      const permission = GLOBAL_PERMISSIONS.rollbackPolicyGuardrails;
      return hasGlobalPermission(this.systemInfo, permission, this.isAdmin);
    },

    policyGuardrailsDecision() {
      const decision = findCapabilityDecision(this.systemInfo, 'policy_guardrails');
      return decision?.access?.some((access) => access === 'read' || access === 'write')
        ? decision : null;
    },

    configured() {
      return !!this.config.endpoint;
    },

    hasCurrentSigningKey() {
      return !!this.config.current_key_id;
    },

    hasNextSigningKey() {
      return !!this.config.next_key_id;
    },

    nextSigningKeyStaged() {
      return this.hasNextSigningKey
        && this.config.next_generation > this.config.current_generation;
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
        {
          text: '', value: 'actions', sortable: false, align: 'end',
        },
      ];
    },

    attemptHeaders() {
      return [
        { text: this.$t('webhookAttempt'), value: 'attempt', sortable: false },
        { text: this.$t('webhookSigningKeyId'), value: 'key_id', sortable: false },
        { text: this.$t('status'), value: 'outcome', sortable: false },
        { text: 'HTTP', value: 'http_status', sortable: false },
        { text: this.$t('lastError'), value: 'reason', sortable: false },
        { text: this.$t('webhookSignedAt'), value: 'signed_at', sortable: false },
      ];
    },
  },

  async created() {
    if (this.showAuditGovernance) {
      await this.load();
    } else {
      this.loading = false;
    }
  },

  methods: {
    async returnToProjects() {
      EventBus.$emit('i-open-last-project');
    },

    async load() {
      this.loading = true;
      this.error = '';
      this.dismissSigningSecret();
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
        current_key_id: config.current_key_id || '',
        next_key_id: config.next_key_id || '',
        current_generation: config.current_generation || 0,
        next_generation: config.next_generation || 0,
        signing_revision: config.signing_revision || 0,
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

    async sendTestDelivery(key = 'current') {
      this.testing = true;
      this.error = '';
      try {
        const response = await axios.post('/api/audit-webhook/test', null, { params: { key } });
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

    applySigningStatus(status) {
      this.config = {
        ...this.config,
        current_key_id: status.current_key_id || '',
        next_key_id: status.next_key_id || '',
        current_generation: status.current_generation || 0,
        next_generation: status.next_generation || 0,
        signing_revision: status.signing_revision || 0,
      };
    },

    revealSigningSecret(result) {
      this.applySigningStatus(result);
      this.signingSecret = result.secret || '';
      this.signingSecretAcknowledged = false;
    },

    dismissSigningSecret() {
      this.signingSecret = '';
      this.signingSecretAcknowledged = false;
    },

    async createSigningSecret() {
      await this.mutateSigningSecret('post', '/api/audit-webhook/signing-secret', true);
    },

    async stageSigningSecret() {
      await this.mutateSigningSecret('post', '/api/audit-webhook/signing-secret/stage', true);
    },

    async promoteSigningSecret() {
      await this.mutateSigningSecret('post', '/api/audit-webhook/signing-secret/promote');
    },

    async revokeSigningSecret() {
      await this.mutateSigningSecret('delete', '/api/audit-webhook/signing-secret/next');
    },

    async mutateSigningSecret(method, url, revealsSecret = false) {
      this.signingMutating = true;
      this.error = '';
      try {
        const options = { params: { revision: this.config.signing_revision || 0 } };
        const response = method === 'delete'
          ? await axios.delete(url, options)
          : await axios.post(url, null, options);
        if (revealsSecret) {
          this.revealSigningSecret(response.data);
        } else {
          this.applySigningStatus(response.data);
        }
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.signingMutating = false;
      }
    },

    async loadAttempts(delivery) {
      this.attemptDelivery = delivery;
      this.attempts = [];
      this.attemptDialog = true;
      this.attemptLoading = true;
      try {
        const response = await axios.get(`/api/audit-webhook/deliveries/${delivery.id}/attempts`, {
          params: { count: 100 },
        });
        this.attempts = response.data || [];
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.attemptLoading = false;
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
        'http_4xx', 'http_5xx', 'invalid_response', 'no_key', 'signing_failure',
        'delivery_failed',
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

.audit-webhook-signing-actions {
  gap: 8px;
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
