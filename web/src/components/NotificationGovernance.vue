<template>
  <section class="notification-governance" data-testid="notification-governance">
    <v-expansion-panels
      v-model="expanded"
      flat
      tile
      @change="onExpansion"
    >
      <v-expansion-panel>
        <v-expansion-panel-header data-testid="notification-governance-toggle">
          <div>
            <div class="subtitle-1">
              <v-icon left>mdi-bell-cog-outline</v-icon>
              {{ $t('notificationGovernance') }}
            </div>
            <div class="text-caption text--secondary">
              {{ $t('notificationGovernanceGlobalOnly') }}
            </div>
          </div>
        </v-expansion-panel-header>
        <v-expansion-panel-content>
          <v-alert
            v-if="unavailable"
            text
            type="info"
            data-testid="notification-governance-unavailable"
          >
            {{ $t('notificationGovernanceUnavailable') }}
          </v-alert>

          <template v-else>
            <v-alert v-if="error" text type="error" dismissible @input="error = ''">
              {{ error }}
            </v-alert>

            <v-card outlined class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon left>mdi-bullhorn-outline</v-icon>
                {{ $t('notificationDestinations') }}
                <v-spacer />
                <v-btn
                  color="primary"
                  small
                  :disabled="loading"
                  @click="openDestination()"
                  data-testid="notification-destination-create"
                >
                  <v-icon left small>mdi-plus</v-icon>
                  {{ $t('add') }}
                </v-btn>
              </v-card-title>
              <v-data-table
                :headers="destinationHeaders"
                :items="destinations"
                :loading="loading"
                :hide-default-footer="true"
                :mobile-breakpoint="720"
                item-key="id"
                data-testid="notification-destinations"
              >
                <template v-slot:item.state="{ item }">
                  <v-chip x-small dark :color="destinationStateColor(item)">
                    {{ destinationStateLabel(item) }}
                  </v-chip>
                </template>
                <template v-slot:item.credential_configured="{ item }">
                  {{ item.credential_configured ? $t('configured') : $t('notConfigured') }}
                </template>
                <template v-slot:item.environment="{ item }">
                  <div>{{ item.environment }}</div>
                  <div
                    v-if="['pagerduty', 'opsgenie'].includes(item.provider) && item.region"
                    class="text-caption text--secondary"
                  >
                    {{ $t('notificationRegion') }}: {{ providerRegionLabel(item.region) }}
                  </div>
                  <div
                    v-if="item.provider === 'opsgenie' && item.opsgenie"
                    class="text-caption text--secondary"
                  >
                    {{ $t('notificationOpsgeniePriority') }}:
                    {{ item.opsgenie.priority || $t('notificationOpsgeniePriorityAutomatic') }}
                    · {{ $t('notificationOpsgenieResponders') }}:
                    {{ (item.opsgenie.responders || []).length }}
                  </div>
                </template>
                <template v-slot:item.actions="{ item }">
                  <div class="notification-governance-row-actions">
                    <v-btn icon small :aria-label="$t('edit')" @click="openDestination(item)">
                      <v-icon small>mdi-pencil</v-icon>
                    </v-btn>
                    <v-btn
                      icon
                      small
                      :loading="testingDestinationId === item.id"
                      :disabled="!canTest(item) || testingDestinationId !== null"
                      :aria-label="$t('notificationTestDelivery')"
                      @click="testDestination(item)"
                    >
                      <v-icon small>mdi-send-check-outline</v-icon>
                    </v-btn>
                    <v-btn
                      icon
                      small
                      :loading="pausingDestinationId === item.id"
                      :disabled="pausingDestinationId !== null"
                      :aria-label="item.paused ? $t('resume') : $t('pause')"
                      @click="setDestinationPaused(item, !item.paused)"
                    >
                      <v-icon small>{{ item.paused ? 'mdi-play' : 'mdi-pause' }}</v-icon>
                    </v-btn>
                  </div>
                </template>
                <template v-slot:no-data>
                  <div class="py-6 grey--text" data-testid="notification-destinations-empty">
                    {{ $t('notificationDestinationsEmpty') }}
                  </div>
                </template>
              </v-data-table>
            </v-card>

            <v-card outlined class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon left>mdi-filter-cog-outline</v-icon>
                {{ $t('notificationRules') }}
                <v-spacer />
                <v-btn
                  color="primary"
                  small
                  :disabled="loading || destinations.length === 0"
                  @click="openRule()"
                  data-testid="notification-rule-create"
                >
                  <v-icon left small>mdi-plus</v-icon>
                  {{ $t('add') }}
                </v-btn>
              </v-card-title>
              <v-data-table
                :headers="ruleHeaders"
                :items="rules"
                :loading="loading"
                :hide-default-footer="true"
                :mobile-breakpoint="720"
                item-key="id"
                data-testid="notification-rules"
              >
                <template v-slot:item.destination_id="{ item }">
                  {{ destinationName(item.destination_id) }}
                </template>
                <template v-slot:item.source_kinds="{ item }">
                  {{ item.source_kinds.join(', ') }}
                </template>
                <template v-slot:item.lifecycle_actions="{ item }">
                  {{ item.lifecycle_actions.join(', ') }}
                </template>
                <template v-slot:item.enabled="{ item }">
                  <v-chip x-small dark :color="item.enabled ? 'success' : 'grey'">
                    {{ item.enabled ? $t('enabled') : $t('disabled') }}
                  </v-chip>
                </template>
                <template v-slot:item.actions="{ item }">
                  <v-btn icon small :aria-label="$t('edit')" @click="openRule(item)">
                    <v-icon small>mdi-pencil</v-icon>
                  </v-btn>
                </template>
                <template v-slot:no-data>
                  <div class="py-6 grey--text" data-testid="notification-rules-empty">
                    {{ $t('notificationRulesEmpty') }}
                  </div>
                </template>
              </v-data-table>
            </v-card>

            <v-card outlined class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon left>mdi-routes</v-icon>
                {{ $t('notificationRoutingPreview') }}
                <v-spacer />
                <v-btn
                  outlined
                  small
                  :loading="previewing"
                  :disabled="previewing"
                  @click="previewRouting"
                  data-testid="notification-preview"
                >
                  <v-icon left small>mdi-eye-outline</v-icon>
                  {{ $t('preview') }}
                </v-btn>
              </v-card-title>
              <v-card-text>
                <p class="text-body-2 mb-3">{{ $t('notificationPreviewDescription') }}</p>
                <v-row dense>
                  <v-col cols="12" sm="4">
                    <v-select
                      v-model="previewSourceKind"
                      :items="sourceKindOptions"
                      :label="$t('notificationSources')"
                      outlined
                      dense
                      hide-details
                      data-testid="notification-preview-source"
                    />
                  </v-col>
                  <v-col cols="12" sm="4">
                    <v-select
                      v-model="previewAction"
                      :items="actionOptions"
                      :label="$t('notificationActions')"
                      outlined
                      dense
                      hide-details
                      data-testid="notification-preview-action"
                    />
                  </v-col>
                  <v-col cols="12" sm="4">
                    <v-select
                      v-model="previewSeverity"
                      :items="severityOptions"
                      :label="$t('notificationMinimumSeverity')"
                      outlined
                      dense
                      hide-details
                      data-testid="notification-preview-severity"
                    />
                  </v-col>
                </v-row>
                <v-list
                  v-if="preview.length > 0"
                  dense
                  class="py-0"
                  data-testid="notification-preview-results"
                >
                  <v-list-item v-for="destination in preview" :key="destination.destination_id">
                    <v-list-item-icon>
                      <v-icon>mdi-check-circle-outline</v-icon>
                    </v-list-item-icon>
                    <v-list-item-content>
                      <v-list-item-title>{{ destination.name }}</v-list-item-title>
                      <v-list-item-subtitle>
                        {{ destination.provider }} · {{ destination.environment }}
                      </v-list-item-subtitle>
                    </v-list-item-content>
                  </v-list-item>
                </v-list>
                <div
                  v-else-if="previewed"
                  class="grey--text"
                  data-testid="notification-preview-empty"
                >
                  {{ $t('notificationPreviewEmpty') }}
                </div>
              </v-card-text>
            </v-card>

            <v-card outlined class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon left>mdi-routes-clock</v-icon>
                {{ $t('notificationEventHistory') }}
                <v-spacer />
                <v-btn
                  icon
                  :loading="eventHistoryLoading"
                  :disabled="eventHistoryLoading"
                  :aria-label="$t('refresh')"
                  @click="loadEventHistory(true)"
                >
                  <v-icon>mdi-refresh</v-icon>
                </v-btn>
              </v-card-title>
              <v-data-table
                :headers="eventHistoryHeaders"
                :items="events"
                :loading="eventHistoryLoading && events.length === 0"
                :hide-default-footer="true"
                :mobile-breakpoint="720"
                item-key="event_id"
                data-testid="notification-event-history"
              >
                <template v-slot:item.source="{ item }">
                  {{ historySourceLabel(item) }}
                </template>
                <template v-slot:item.lifecycle="{ item }">
                  {{ historyLifecycleLabel(item) }}
                </template>
                <template v-slot:item.routing_outcome="{ item }">
                  {{ routingOutcomeLabel(item.routing_outcome) }}
                </template>
                <template v-slot:item.occurred_at="{ item }">
                  {{ formatTimestamp(item.occurred_at) }}
                </template>
                <template v-slot:no-data>
                  <div class="py-6 grey--text" data-testid="notification-event-history-empty">
                    {{ $t('notificationEventHistoryEmpty') }}
                  </div>
                </template>
              </v-data-table>
              <v-card-actions v-if="eventHistoryHasMore" class="justify-center">
                <v-btn
                  text
                  :loading="eventHistoryLoading"
                  :disabled="eventHistoryLoading"
                  @click="loadEventHistory(false)"
                >
                  {{ $t('loadMore') }}
                </v-btn>
              </v-card-actions>
            </v-card>

            <v-card outlined>
              <v-card-title class="subtitle-1">
                <v-icon left>mdi-history</v-icon>
                {{ $t('notificationDeliveryHistory') }}
                <v-spacer />
                <v-btn
                  icon
                  :loading="historyLoading"
                  :disabled="historyLoading"
                  :aria-label="$t('refresh')"
                  @click="loadHistory(true)"
                >
                  <v-icon>mdi-refresh</v-icon>
                </v-btn>
              </v-card-title>
              <v-data-table
                :headers="historyHeaders"
                :items="deliveries"
                :loading="historyLoading && deliveries.length === 0"
                :hide-default-footer="true"
                :mobile-breakpoint="720"
                item-key="id"
                data-testid="notification-history"
              >
                <template v-slot:item.destination_name="{ item }">
                  <div>{{ item.destination_name }}</div>
                  <div class="text-caption text--secondary">
                    {{ item.destination_environment }}
                    <template v-if="item.destination_region">
                      · {{ providerRegionLabel(item.destination_region) }}
                    </template>
                  </div>
                  <div class="text-caption text--secondary notification-governance-incident-key">
                    {{ $t('notificationIncidentKey') }}: {{ item.incident_key }}
                  </div>
                  <div
                    v-if="item.provider_request_id"
                    class="text-caption text--secondary notification-governance-incident-key"
                  >
                    {{ $t('notificationProviderRequestID') }}: {{ item.provider_request_id }}
                  </div>
                </template>
                <template v-slot:item.status="{ item }">
                  <v-chip x-small dark :color="deliveryStatusColor(item.status)">
                    {{ item.status }}
                  </v-chip>
                </template>
                <template v-slot:item.updated_at="{ item }">
                  {{ formatTimestamp(item.updated_at) }}
                </template>
                <template v-slot:item.source="{ item }">
                  {{ historySourceLabel(item) }}
                </template>
                <template v-slot:item.lifecycle="{ item }">
                  {{ historyLifecycleLabel(item) }}
                </template>
                <template v-slot:item.last_reason="{ item }">
                  {{ deliveryReasonLabel(item.last_reason) }}
                </template>
                <template v-slot:item.actions="{ item }">
                  <v-btn
                    v-if="item.status === 'failed'"
                    text
                    small
                    color="primary"
                    :loading="retryingDeliveryId === item.id"
                    :disabled="retryingDeliveryId !== null"
                    @click="retryDelivery(item)"
                    :data-testid="`notification-retry-${item.id}`"
                  >{{ $t('retry') }}</v-btn>
                </template>
                <template v-slot:no-data>
                  <div class="py-6 grey--text" data-testid="notification-history-empty">
                    {{ $t('notificationHistoryEmpty') }}
                  </div>
                </template>
              </v-data-table>
              <v-card-actions v-if="historyHasMore" class="justify-center">
                <v-btn
                  text
                  :loading="historyLoading"
                  :disabled="historyLoading"
                  @click="loadHistory(false)"
                >
                  {{ $t('loadMore') }}
                </v-btn>
              </v-card-actions>
            </v-card>
          </template>
        </v-expansion-panel-content>
      </v-expansion-panel>
    </v-expansion-panels>

    <v-dialog v-model="destinationDialog" max-width="640" persistent>
      <v-card>
        <v-card-title>
          {{ destinationDialogTitle }}
        </v-card-title>
        <v-card-text>
          <v-form ref="destinationForm" @submit.prevent="saveDestination">
            <v-text-field
              v-model.trim="destinationForm.name"
              :label="$t('name')"
              :rules="requiredRules"
              outlined
              dense
              :disabled="destinationSaving"
            />
            <v-text-field
              v-model.trim="destinationForm.provider"
              :label="$t('notificationProvider')"
              :rules="requiredRules"
              outlined
              dense
              :disabled="destinationSaving"
            />
            <v-text-field
              v-model.trim="destinationForm.environment"
              :label="$t('notificationEnvironment')"
              :rules="requiredRules"
              outlined
              dense
              :disabled="destinationSaving"
            />
            <v-select
              v-if="['pagerduty', 'opsgenie'].includes(destinationForm.provider.trim())"
              v-model="destinationForm.region"
              :items="providerRegionOptions"
              item-text="text"
              item-value="value"
              :label="$t('notificationRegion')"
              :rules="requiredRules"
              outlined
              dense
              :disabled="destinationSaving"
              data-testid="notification-destination-region"
            />
            <v-select
              v-if="destinationForm.provider.trim() === 'opsgenie'"
              v-model="destinationForm.opsgeniePriority"
              :items="opsgeniePriorityOptions"
              item-text="text"
              item-value="value"
              :label="$t('notificationOpsgeniePriority')"
              outlined
              dense
              :disabled="destinationSaving"
              data-testid="notification-destination-opsgenie-priority"
            />
            <v-textarea
              v-if="destinationForm.provider.trim() === 'opsgenie'"
              v-model="destinationForm.opsgenieRespondersText"
              :label="$t('notificationOpsgenieResponders')"
              :hint="$t('notificationOpsgenieRespondersHint')"
              :rules="opsgenieResponderRules"
              persistent-hint
              outlined
              dense
              rows="3"
              :disabled="destinationSaving"
              data-testid="notification-destination-opsgenie-responders"
            />
            <v-text-field
              v-model="destinationForm.credential"
              :label="$t('notificationCredential')"
              :rules="destinationCredentialRules"
              type="password"
              autocomplete="new-password"
              :hint="$t('notificationCredentialPreserved')"
              persistent-hint
              outlined
              dense
              :disabled="destinationSaving"
              data-testid="notification-destination-credential"
            />
            <v-checkbox
              v-model="destinationForm.enabled"
              :label="$t('enabled')"
              dense
              :disabled="destinationSaving"
            />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text :disabled="destinationSaving" @click="closeDestination">
            {{ $t('cancel') }}
          </v-btn>
          <v-btn
            color="primary"
            :loading="destinationSaving"
            :disabled="destinationSaving"
            @click="saveDestination"
            data-testid="notification-destination-save"
          >
            {{ $t('save') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="ruleDialog" max-width="640" persistent>
      <v-card>
        <v-card-title>
          {{ ruleForm.id ? $t('editNotificationRule') : $t('addNotificationRule') }}
        </v-card-title>
        <v-card-text>
          <v-form ref="ruleForm" @submit.prevent="saveRule">
            <v-select
              v-model="ruleForm.destination_id"
              :items="destinationOptions"
              item-text="text"
              item-value="value"
              :label="$t('notificationDestination')"
              :rules="requiredRules"
              outlined
              dense
              :disabled="ruleSaving"
            />
            <v-select
              v-model="ruleForm.source_kinds"
              :items="sourceKindOptions"
              :label="$t('notificationSources')"
              multiple
              outlined
              dense
              :disabled="ruleSaving"
            />
            <v-select
              v-model="ruleForm.lifecycle_actions"
              :items="actionOptions"
              :label="$t('notificationActions')"
              multiple
              outlined
              dense
              :disabled="ruleSaving"
            />
            <v-select
              v-model="ruleForm.minimum_severity"
              :items="severityOptions"
              :label="$t('notificationMinimumSeverity')"
              outlined
              dense
              :disabled="ruleSaving"
            />
            <v-checkbox
              v-model="ruleForm.enabled"
              :label="$t('enabled')"
              dense
              :disabled="ruleSaving"
            />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text :disabled="ruleSaving" @click="closeRule">{{ $t('cancel') }}</v-btn>
          <v-btn
            color="primary"
            :loading="ruleSaving"
            :disabled="ruleSaving"
            @click="saveRule"
            data-testid="notification-rule-save"
          >
            {{ $t('save') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </section>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';

const PAGE_SIZE = 25;
const BASE_URL = '/api/notification-governance';

const parseOpsgenieResponders = (value) => {
  const lines = value.split('\n').map((line) => line.trim()).filter(Boolean);
  if (lines.length > 50) return null;
  return lines.reduce((responders, line) => {
    if (responders === null) return null;
    const match = /^(team|user|escalation|schedule):(id|name|username):([^\r\n]{1,256})$/.exec(line);
    if (!match) return null;
    const [, type, field, identity] = match;
    if (identity.trim() !== identity
      || (type === 'user' ? !['id', 'username'].includes(field) : !['id', 'name'].includes(field))) return null;
    return [...responders, { type, [field]: identity }];
  }, []);
};

const formatOpsgenieResponders = (responders = []) => responders.map((responder) => {
  const field = ['id', 'name', 'username'].find((candidate) => responder[candidate]);
  return field ? `${responder.type}:${field}:${responder[field]}` : '';
}).filter(Boolean).join('\n');

const newDestinationForm = () => ({
  id: null,
  revision: 0,
  name: '',
  provider: '',
  environment: '',
  region: 'us',
  opsgeniePriority: '',
  opsgenieRespondersText: '',
  credential: '',
  enabled: true,
});

const newRuleForm = () => ({
  id: null,
  revision: 0,
  destination_id: null,
  source_kinds: ['system'],
  lifecycle_actions: ['trigger'],
  minimum_severity: 'info',
  enabled: true,
});

export default {
  name: 'NotificationGovernance',

  data() {
    return {
      expanded: null,
      loaded: false,
      loading: false,
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
      preview: [],
      previewed: false,
      previewing: false,
      previewSourceKind: 'system',
      previewAction: 'trigger',
      previewSeverity: 'info',
      destinationDialog: false,
      destinationSaving: false,
      destinationForm: newDestinationForm(),
      ruleDialog: false,
      ruleSaving: false,
      ruleForm: newRuleForm(),
      testingDestinationId: null,
      pausingDestinationId: null,
      retryingDeliveryId: null,
      requiredRules: [(value) => !!value || this.$t('required')],
    };
  },

  computed: {
    destinationDialogTitle() {
      return this.destinationForm.id
        ? this.$t('editNotificationDestination')
        : this.$t('addNotificationDestination');
    },
    destinationHeaders() {
      return [
        { text: this.$t('name'), value: 'name', sortable: false },
        { text: this.$t('notificationProvider'), value: 'provider', sortable: false },
        { text: this.$t('notificationEnvironment'), value: 'environment', sortable: false },
        { text: this.$t('notificationCredential'), value: 'credential_configured', sortable: false },
        { text: this.$t('status'), value: 'state', sortable: false },
        {
          text: '', value: 'actions', sortable: false, align: 'end',
        },
      ];
    },
    ruleHeaders() {
      return [
        { text: this.$t('notificationDestination'), value: 'destination_id', sortable: false },
        { text: this.$t('notificationSources'), value: 'source_kinds', sortable: false },
        { text: this.$t('notificationActions'), value: 'lifecycle_actions', sortable: false },
        { text: this.$t('notificationMinimumSeverity'), value: 'minimum_severity', sortable: false },
        { text: this.$t('status'), value: 'enabled', sortable: false },
        {
          text: '', value: 'actions', sortable: false, align: 'end',
        },
      ];
    },
    historyHeaders() {
      return [
        { text: this.$t('notificationHistorySource'), value: 'source', sortable: false },
        { text: this.$t('notificationHistoryLifecycle'), value: 'lifecycle', sortable: false },
        { text: this.$t('notificationDestination'), value: 'destination_name', sortable: false },
        { text: this.$t('status'), value: 'status', sortable: false },
        { text: this.$t('attempts'), value: 'attempts', sortable: false },
        { text: this.$t('notificationHistoryReason'), value: 'last_reason', sortable: false },
        { text: this.$t('updated'), value: 'updated_at', sortable: false },
        {
          text: '', value: 'actions', sortable: false, align: 'end',
        },
      ];
    },
    eventHistoryHeaders() {
      return [
        { text: this.$t('notificationHistorySource'), value: 'source', sortable: false },
        { text: this.$t('notificationHistoryLifecycle'), value: 'lifecycle', sortable: false },
        { text: this.$t('notificationRoutingOutcome'), value: 'routing_outcome', sortable: false },
        { text: this.$t('notificationOccurred'), value: 'occurred_at', sortable: false },
      ];
    },
    destinationOptions() {
      return this.destinations.map((destination) => ({
        text: destination.name,
        value: destination.id,
      }));
    },
    providerRegionOptions() {
      return [
        { text: this.$t('notificationRegionUS'), value: 'us' },
        { text: this.$t('notificationRegionEU'), value: 'eu' },
      ];
    },
    opsgeniePriorityOptions() {
      return [
        { text: this.$t('notificationOpsgeniePriorityAutomatic'), value: '' },
        ...['P1', 'P2', 'P3', 'P4', 'P5'].map((value) => ({ text: value, value })),
      ];
    },
    destinationCredentialRules() {
      if (this.destinationForm.credential === '') return [];
      if (this.destinationForm.provider.trim() === 'pagerduty') {
        return [(value) => value.length === 32 || this.$t('notificationPagerDutyKeyLength')];
      }
      if (this.destinationForm.provider.trim() === 'opsgenie') {
        return [(value) => /^[\x21-\x7e]{1,256}$/.test(value) || this.$t('notificationOpsgenieKeyInvalid')];
      }
      return [];
    },
    opsgenieResponderRules() {
      if (this.destinationForm.provider.trim() !== 'opsgenie') return [];
      return [
        (value) => parseOpsgenieResponders(value) !== null
          || this.$t('notificationOpsgenieRespondersInvalid'),
      ];
    },
    sourceKindOptions() {
      return ['task', 'workflow', 'approval', 'system'];
    },
    actionOptions() {
      return ['trigger', 'update', 'resolve'];
    },
    severityOptions() {
      return ['info', 'warning', 'error', 'critical'];
    },
  },

  methods: {
    async onExpansion(value) {
      if (value === 0 && !this.loaded && !this.loading) {
        await this.load();
      }
    },

    async load() {
      this.loading = true;
      this.error = '';
      try {
        const [destinations, rules] = await Promise.all([
          axios.get(`${BASE_URL}/destinations`, { params: { count: PAGE_SIZE, offset: 0 } }),
          axios.get(`${BASE_URL}/rules`, { params: { count: PAGE_SIZE, offset: 0 } }),
        ]);
        this.destinations = destinations.data;
        this.rules = rules.data;
        this.loaded = true;
        await this.loadHistory(true);
        await this.loadEventHistory(true);
      } catch (err) {
        this.handleError(err);
      } finally {
        this.loading = false;
      }
    },

    async loadHistory(reset) {
      if (this.unavailable || this.historyLoading) return;
      this.historyLoading = true;
      try {
        const offset = reset ? 0 : this.deliveries.length;
        const response = await axios.get(`${BASE_URL}/deliveries`, { params: { count: PAGE_SIZE, offset } });
        this.deliveries = reset ? response.data : this.deliveries.concat(response.data);
        this.historyHasMore = response.data.length === PAGE_SIZE;
      } catch (err) {
        this.handleError(err);
      } finally {
        this.historyLoading = false;
      }
    },

    async loadEventHistory(reset) {
      if (this.unavailable || this.eventHistoryLoading) return;
      this.eventHistoryLoading = true;
      try {
        const offset = reset ? 0 : this.events.length;
        const response = await axios.get(`${BASE_URL}/events`, { params: { count: PAGE_SIZE, offset } });
        this.events = reset ? response.data : this.events.concat(response.data);
        this.eventHistoryHasMore = response.data.length === PAGE_SIZE;
      } catch (err) {
        this.handleError(err);
      } finally {
        this.eventHistoryLoading = false;
      }
    },

    openDestination(destination) {
      this.destinationForm = destination ? {
        id: destination.id,
        revision: destination.revision,
        name: destination.name,
        provider: destination.provider,
        environment: destination.environment,
        region: destination.region || 'us',
        opsgeniePriority: destination.opsgenie?.priority || '',
        opsgenieRespondersText: formatOpsgenieResponders(destination.opsgenie?.responders),
        credential: '',
        enabled: !!destination.enabled,
      } : newDestinationForm();
      this.destinationDialog = true;
    },

    closeDestination(force = false) {
      if (this.destinationSaving && !force) return;
      this.destinationDialog = false;
      this.destinationForm = newDestinationForm();
    },

    async saveDestination() {
      if (this.destinationSaving
        || (this.$refs.destinationForm && !this.$refs.destinationForm.validate())) return;
      this.destinationSaving = true;
      this.error = '';
      try {
        const payload = {
          name: this.destinationForm.name.trim(),
          provider: this.destinationForm.provider.trim(),
          environment: this.destinationForm.environment.trim(),
          region: ['pagerduty', 'opsgenie'].includes(this.destinationForm.provider.trim())
            ? this.destinationForm.region
            : '',
          enabled: this.destinationForm.enabled,
        };
        if (this.destinationForm.provider.trim() === 'opsgenie') {
          payload.opsgenie = {
            priority: this.destinationForm.opsgeniePriority,
            responders: parseOpsgenieResponders(this.destinationForm.opsgenieRespondersText),
          };
        }
        if (this.destinationForm.credential !== '') {
          payload.credential = this.destinationForm.credential;
        }
        const response = this.destinationForm.id
          ? await axios.put(`${BASE_URL}/destinations/${this.destinationForm.id}`, {
            ...payload,
            revision: this.destinationForm.revision,
          })
          : await axios.post(`${BASE_URL}/destinations`, payload);
        this.upsertDestination(response.data);
        this.closeDestination(true);
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('notificationDestinationSaved'),
        });
      } catch (err) {
        this.handleError(err);
      } finally {
        this.destinationForm.credential = '';
        this.destinationSaving = false;
      }
    },

    async setDestinationPaused(destination, paused) {
      if (this.pausingDestinationId !== null) return;
      this.pausingDestinationId = destination.id;
      this.error = '';
      try {
        const action = paused ? 'pause' : 'resume';
        const response = await axios.post(`${BASE_URL}/destinations/${destination.id}/${action}`, {
          revision: destination.revision,
        });
        this.upsertDestination(response.data);
        EventBus.$emit('i-snackbar', {
          color: paused ? 'warning' : 'success',
          text: paused ? this.$t('notificationPaused') : this.$t('notificationResumed'),
        });
      } catch (err) {
        this.handleError(err);
      } finally {
        this.pausingDestinationId = null;
      }
    },

    async testDestination(destination) {
      if (this.testingDestinationId !== null || !this.canTest(destination)) return;
      this.testingDestinationId = destination.id;
      this.error = '';
      try {
        await axios.post(`${BASE_URL}/destinations/${destination.id}/test`);
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('notificationTestQueued'),
        });
        await this.loadHistory(true);
      } catch (err) {
        this.handleError(err);
      } finally {
        this.testingDestinationId = null;
      }
    },

    openRule(rule) {
      this.ruleForm = rule ? {
        id: rule.id,
        revision: rule.revision,
        destination_id: rule.destination_id,
        source_kinds: [...rule.source_kinds],
        lifecycle_actions: [...rule.lifecycle_actions],
        minimum_severity: rule.minimum_severity,
        enabled: !!rule.enabled,
      } : newRuleForm();
      this.ruleDialog = true;
    },

    closeRule(force = false) {
      if (this.ruleSaving && !force) return;
      this.ruleDialog = false;
      this.ruleForm = newRuleForm();
    },

    async saveRule() {
      if (this.ruleSaving || (this.$refs.ruleForm && !this.$refs.ruleForm.validate())) return;
      this.ruleSaving = true;
      this.error = '';
      try {
        const payload = {
          destination_id: this.ruleForm.destination_id,
          source_kinds: this.ruleForm.source_kinds,
          lifecycle_actions: this.ruleForm.lifecycle_actions,
          minimum_severity: this.ruleForm.minimum_severity,
          enabled: this.ruleForm.enabled,
        };
        const response = this.ruleForm.id
          ? await axios.put(`${BASE_URL}/rules/${this.ruleForm.id}`, {
            ...payload,
            revision: this.ruleForm.revision,
          })
          : await axios.post(`${BASE_URL}/rules`, payload);
        this.upsertRule(response.data);
        this.closeRule(true);
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('notificationRuleSaved'),
        });
      } catch (err) {
        this.handleError(err);
      } finally {
        this.ruleSaving = false;
      }
    },

    previewEvent() {
      const event = {
        schema_version: 'semaphore.notification.v1',
        source_revision: 1,
        scope: 'global',
        severity: this.previewSeverity,
        lifecycle_action: this.previewAction,
        details: {},
      };
      const terminal = this.previewAction === 'resolve';
      let status = 'failed';
      if (terminal) status = 'succeeded';
      else if (this.previewAction === 'update') status = 'running';
      if (this.previewSourceKind === 'task') {
        return {
          ...event,
          source: { kind: 'task', id: 'task:9001' },
          lifecycle_id: 'template:9002',
          details: { task_id: 9001, template_id: 9002, status },
        };
      }
      if (this.previewSourceKind === 'workflow') {
        return {
          ...event,
          source: { kind: 'workflow', id: 'workflow_run:9001' },
          lifecycle_id: 'workflow:9002',
          details: { workflow_id: 9002, workflow_run_id: 9001, status },
        };
      }
      if (this.previewSourceKind === 'approval') {
        return {
          ...event,
          source: { kind: 'approval', id: 'approval:9001' },
          lifecycle_id: 'approval:9001',
          details: {
            workflow_id: 9002,
            workflow_run_id: 9003,
            approval_id: 9001,
            ...(terminal ? { decision: 'approved' } : { status: 'pending' }),
          },
        };
      }
      return {
        ...event,
        source: { kind: 'system', id: 'system:notification-preview' },
        lifecycle_id: 'system:notification-preview',
      };
    },

    async previewRouting() {
      if (this.previewing) return;
      this.previewing = true;
      this.error = '';
      try {
        const response = await axios.post(
          `${BASE_URL}/routing/preview`,
          this.previewEvent(),
        );
        this.preview = response.data;
        this.previewed = true;
      } catch (err) {
        this.handleError(err);
      } finally {
        this.previewing = false;
      }
    },

    async retryDelivery(delivery) {
      if (this.retryingDeliveryId !== null || delivery.status !== 'failed') return;
      this.retryingDeliveryId = delivery.id;
      this.error = '';
      try {
        const response = await axios.post(`${BASE_URL}/deliveries/${delivery.id}/retry`);
        this.upsertDelivery(response.data);
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('notificationRetryQueued'),
        });
      } catch (err) {
        this.handleError(err);
      } finally {
        this.retryingDeliveryId = null;
      }
    },

    upsertDestination(destination) {
      const index = this.destinations.findIndex((item) => item.id === destination.id);
      if (index === -1) this.destinations = [...this.destinations, destination];
      else this.destinations.splice(index, 1, destination);
    },
    upsertRule(rule) {
      const index = this.rules.findIndex((item) => item.id === rule.id);
      if (index === -1) this.rules = [...this.rules, rule];
      else this.rules.splice(index, 1, rule);
    },
    upsertDelivery(delivery) {
      const index = this.deliveries.findIndex((item) => item.id === delivery.id);
      if (index === -1) this.deliveries = [delivery, ...this.deliveries];
      else this.deliveries.splice(index, 1, delivery);
    },
    destinationName(id) {
      const destination = this.destinations.find((item) => item.id === id);
      return destination ? destination.name : `#${id}`;
    },
    providerRegionLabel(region) {
      if (region === 'us') return this.$t('notificationRegionUS');
      if (region === 'eu') return this.$t('notificationRegionEU');
      return '—';
    },
    canTest(destination) {
      return destination.enabled && !destination.paused && destination.credential_configured;
    },
    destinationStateColor(destination) {
      if (!destination.enabled) return 'grey';
      return destination.paused ? 'warning' : 'success';
    },
    destinationStateLabel(destination) {
      if (!destination.enabled) return this.$t('disabled');
      return destination.paused ? this.$t('paused') : this.$t('active');
    },
    deliveryStatusColor(status) {
      return {
        pending: 'grey', delivering: 'blue', retrying: 'warning', succeeded: 'success', failed: 'error',
      }[status] || 'grey';
    },
    historySourceLabel(item) {
      const labels = {
        task: 'notificationSourceTask',
        workflow: 'notificationSourceWorkflow',
        approval: 'notificationSourceApproval',
        system: 'notificationSourceSystem',
      };
      if (!labels[item.source_kind] || !item.source_id) return '—';
      return `${this.$t(labels[item.source_kind])} · ${item.source_id}`;
    },
    historyLifecycleLabel(item) {
      const actions = {
        trigger: 'notificationActionTrigger',
        update: 'notificationActionUpdate',
        resolve: 'notificationActionResolve',
      };
      const severities = {
        info: 'notificationSeverityInfo',
        warning: 'notificationSeverityWarning',
        error: 'notificationSeverityError',
        critical: 'notificationSeverityCritical',
      };
      if (!actions[item.lifecycle_action] || !severities[item.severity]) return '—';
      return `${this.$t(actions[item.lifecycle_action])} · ${this.$t(severities[item.severity])}`;
    },
    routingOutcomeLabel(outcome) {
      if (outcome === 'routed') return this.$t('notificationRoutingOutcomeRouted');
      if (outcome === 'filtered') return this.$t('notificationRoutingOutcomeFiltered');
      return '—';
    },
    deliveryReasonLabel(reason) {
      const labels = {
        configuration_error: 'notificationReasonConfiguration',
        rate_limited: 'notificationReasonRateLimited',
        transport_error: 'notificationReasonTransport',
        attempts_exhausted: 'notificationReasonAttemptsExhausted',
        manual_retry: 'notificationReasonManualRetry',
        destination_paused: 'notificationReasonDestinationPaused',
        destination_missing: 'notificationReasonDestinationMissing',
        destination_disabled: 'notificationReasonDestinationDisabled',
        destination_revision_changed: 'notificationReasonDestinationChanged',
        credential_unavailable: 'notificationReasonCredentialUnavailable',
        provider_unavailable: 'notificationReasonProviderUnavailable',
        provider_pending: 'notificationReasonProviderPending',
        permanent_failure: 'notificationReasonPermanent',
      };
      return labels[reason] ? this.$t(labels[reason]) : '—';
    },
    formatTimestamp(value) {
      if (!value || value.startsWith('0001-01-01')) return '—';
      return new Date(value).toLocaleString();
    },
    handleError(err) {
      const status = err && err.response ? err.response.status : 0;
      if (status === 503) {
        this.unavailable = true;
        this.error = '';
      } else if (status === 409) {
        this.error = this.$t('notificationConflict');
      } else if (status === 400) {
        this.error = this.$t('notificationValidationError');
      } else if (status === 401 || status === 403) {
        this.error = this.$t('notificationPermissionDenied');
      } else if (status === 404) {
        this.error = this.$t('notificationResourceUnavailable');
      } else {
        this.error = this.$t('notificationRequestFailed');
      }
    },
  },
};
</script>

<style scoped>
.notification-governance {
  margin-top: 16px;
}

.notification-governance-row-actions {
  align-items: center;
  display: flex;
  justify-content: flex-end;
  min-width: 108px;
}

.notification-governance-incident-key {
  overflow-wrap: anywhere;
}

@media (max-width: 720px) {
  .notification-governance-row-actions {
    justify-content: flex-start;
  }
}
</style>
