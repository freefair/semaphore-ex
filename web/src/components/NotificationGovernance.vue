<template>
  <section class="notification-governance" data-testid="notification-governance">
    <v-expansion-panels
      v-model="expanded"
      flat
      tile
      @update:model-value="onExpansion"
    >
      <v-expansion-panel>
        <v-expansion-panel-title data-testid="notification-governance-toggle">
          <div>
            <div class="subtitle-1">
              <v-icon start>mdi-bell-cog-outline</v-icon>
              {{ $t('notificationGovernance') }}
            </div>
            <div class="text-body-small text--secondary">
              {{ $t('notificationGovernanceGlobalOnly') }}
            </div>
          </div>
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <v-alert
            v-if="unavailable"
            variant="tonal"
            type="info"
            data-testid="notification-governance-unavailable"
          >
            {{ $t('notificationGovernanceUnavailable') }}
          </v-alert>

          <template v-else>
            <v-alert
              v-if="error"
              variant="tonal"
              type="error"
              dismissible
              @update:model-value="error = ''"
            >
              {{ error }}
            </v-alert>

            <v-card variant="outlined" class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon start>mdi-bullhorn-outline</v-icon>
                {{ $t('notificationDestinations') }}
                <v-spacer />
                <v-btn
                  color="primary"
                  size="small"
                  :disabled="loading"
                  @click="openDestination()"
                  data-testid="notification-destination-create"
                >
                  <v-icon start size="small">mdi-plus</v-icon>
                  {{ $t('add') }}
                </v-btn>
              </v-card-title>
              <v-data-table
                :headers="destinationHeaders"
                :items="destinations"
                :loading="loading"
                :hide-default-footer="true"
                :mobile-breakpoint="720"
                item-value="id"
                data-testid="notification-destinations"
              >
                <template v-slot:item.state="{ item }">
                  <v-chip size="x-small" dark :color="destinationStateColor(item)">
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
                    class="text-body-small text--secondary"
                  >
                    {{ $t('notificationRegion') }}: {{ providerRegionLabel(item.region) }}
                  </div>
                  <div
                    v-if="item.provider === 'opsgenie' && item.opsgenie"
                    class="text-body-small text--secondary"
                  >
                    {{ $t('notificationOpsgeniePriority') }}:
                    {{ item.opsgenie.priority || $t('notificationOpsgeniePriorityAutomatic') }}
                    · {{ $t('notificationOpsgenieResponders') }}:
                    {{ (item.opsgenie.responders || []).length }}
                  </div>
                  <div
                    v-if="item.provider === 'servicenow' && item.servicenow"
                    class="text-body-small text--secondary notification-governance-incident-key"
                  >
                    {{ item.servicenow.instance_origin }}
                    · {{ serviceNowAuthModeLabel(item.servicenow.auth_mode) }}
                  </div>
                </template>
                <template v-slot:item.actions="{ item }">
                  <div class="notification-governance-row-actions">
                    <v-btn
                      icon
                      size="small"
                      :aria-label="$t('edit')"
                      @click="openDestination(item)"
                    >
                      <v-icon size="small">mdi-pencil</v-icon>
                    </v-btn>
                    <v-btn
                      icon
                      size="small"
                      :loading="testingDestinationId === item.id"
                      :disabled="!canTest(item) || testingDestinationId !== null"
                      :aria-label="$t('notificationTestDelivery')"
                      @click="testDestination(item)"
                    >
                      <v-icon size="small">mdi-send-check-outline</v-icon>
                    </v-btn>
                    <v-btn
                      icon
                      size="small"
                      :loading="pausingDestinationId === item.id"
                      :disabled="pausingDestinationId !== null"
                      :aria-label="item.paused ? $t('resume') : $t('pause')"
                      @click="setDestinationPaused(item, !item.paused)"
                    >
                      <v-icon size="small">{{ item.paused ? 'mdi-play' : 'mdi-pause' }}</v-icon>
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

            <v-card variant="outlined" class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon start>mdi-filter-cog-outline</v-icon>
                {{ $t('notificationRules') }}
                <v-spacer />
                <v-btn
                  color="primary"
                  size="small"
                  :disabled="loading || destinations.length === 0"
                  @click="openRule()"
                  data-testid="notification-rule-create"
                >
                  <v-icon start size="small">mdi-plus</v-icon>
                  {{ $t('add') }}
                </v-btn>
              </v-card-title>
              <v-data-table
                :headers="ruleHeaders"
                :items="rules"
                :loading="loading"
                :hide-default-footer="true"
                :mobile-breakpoint="720"
                item-value="id"
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
                  <v-chip size="x-small" dark :color="item.enabled ? 'success' : 'grey'">
                    {{ item.enabled ? $t('enabled') : $t('disabled') }}
                  </v-chip>
                </template>
                <template v-slot:item.actions="{ item }">
                  <v-btn icon size="small" :aria-label="$t('edit')" @click="openRule(item)">
                    <v-icon size="small">mdi-pencil</v-icon>
                  </v-btn>
                </template>
                <template v-slot:no-data>
                  <div class="py-6 grey--text" data-testid="notification-rules-empty">
                    {{ $t('notificationRulesEmpty') }}
                  </div>
                </template>
              </v-data-table>
            </v-card>

            <v-card variant="outlined" class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon start>mdi-routes</v-icon>
                {{ $t('notificationRoutingPreview') }}
                <v-spacer />
                <v-btn
                  variant="outlined"
                  size="small"
                  :loading="previewing"
                  :disabled="previewing"
                  @click="previewRouting"
                  data-testid="notification-preview"
                >
                  <v-icon start size="small">mdi-eye-outline</v-icon>
                  {{ $t('preview') }}
                </v-btn>
              </v-card-title>
              <v-card-text>
                <p class="text-body-medium mb-3">{{ $t('notificationPreviewDescription') }}</p>
                <v-row density="compact">
                  <v-col cols="12" sm="4">
                    <v-select
                      v-model="previewSourceKind"
                      :items="sourceKindOptions"
                      :label="$t('notificationSources')"
                      variant="outlined"
                      density="compact"
                      hide-details
                      data-testid="notification-preview-source"
                    />
                  </v-col>
                  <v-col cols="12" sm="4">
                    <v-select
                      v-model="previewAction"
                      :items="actionOptions"
                      :label="$t('notificationActions')"
                      variant="outlined"
                      density="compact"
                      hide-details
                      data-testid="notification-preview-action"
                    />
                  </v-col>
                  <v-col cols="12" sm="4">
                    <v-select
                      v-model="previewSeverity"
                      :items="severityOptions"
                      :label="$t('notificationMinimumSeverity')"
                      variant="outlined"
                      density="compact"
                      hide-details
                      data-testid="notification-preview-severity"
                    />
                  </v-col>
                </v-row>
                <v-list
                  v-if="preview.length > 0"
                  density="compact"
                  class="py-0"
                  data-testid="notification-preview-results"
                >
                  <v-list-item v-for="destination in preview" :key="destination.destination_id">
                    <template #prepend>
                      <v-icon>mdi-check-circle-outline</v-icon>
                    </template>
                    <div>
                      <v-list-item-title>{{ destination.name }}</v-list-item-title>
                      <v-list-item-subtitle>
                        {{ destination.provider }} · {{ destination.environment }}
                      </v-list-item-subtitle>
                    </div>
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

            <v-card variant="outlined" class="mb-4">
              <v-card-title class="subtitle-1">
                <v-icon start>mdi-routes-clock</v-icon>
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
                item-value="event_id"
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
                  variant="text"
                  :loading="eventHistoryLoading"
                  :disabled="eventHistoryLoading"
                  @click="loadEventHistory(false)"
                >
                  {{ $t('loadMore') }}
                </v-btn>
              </v-card-actions>
            </v-card>

            <v-card variant="outlined">
              <v-card-title class="subtitle-1">
                <v-icon start>mdi-history</v-icon>
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
                item-value="id"
                data-testid="notification-history"
              >
                <template v-slot:item.destination_name="{ item }">
                  <div>{{ item.destination_name }}</div>
                  <div class="text-body-small text--secondary">
                    {{ item.destination_environment }}
                    <template v-if="item.destination_region">
                      · {{ providerRegionLabel(item.destination_region) }}
                    </template>
                  </div>
                  <div class="text-body-small text--secondary notification-governance-incident-key">
                    {{ $t('notificationIncidentKey') }}: {{ item.incident_key }}
                  </div>
                  <div
                    v-if="item.provider_request_id"
                    class="text-body-small text--secondary notification-governance-incident-key"
                  >
                    {{ $t('notificationProviderRequestID') }}: {{ item.provider_request_id }}
                  </div>
                  <div
                    v-if="item.provider_record_id"
                    class="text-body-small text--secondary notification-governance-incident-key"
                  >
                    {{ $t('notificationProviderRecordID') }}:
                    <a
                      v-if="safeProviderRecordURL(item)"
                      :href="safeProviderRecordURL(item)"
                      target="_blank"
                      rel="noopener noreferrer"
                    >{{ item.provider_record_id }}</a>
                    <span v-else>{{ item.provider_record_id }}</span>
                  </div>
                </template>
                <template v-slot:item.status="{ item }">
                  <v-chip size="x-small" dark :color="deliveryStatusColor(item.status)">
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
                    variant="text"
                    size="small"
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
                  variant="text"
                  :loading="historyLoading"
                  :disabled="historyLoading"
                  @click="loadHistory(false)"
                >
                  {{ $t('loadMore') }}
                </v-btn>
              </v-card-actions>
            </v-card>
          </template>
        </v-expansion-panel-text>
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
              variant="outlined"
              density="compact"
              :disabled="destinationSaving"
            />
            <v-text-field
              v-model.trim="destinationForm.provider"
              :label="$t('notificationProvider')"
              :rules="requiredRules"
              variant="outlined"
              density="compact"
              :disabled="destinationSaving"
            />
            <v-text-field
              v-model.trim="destinationForm.environment"
              :label="$t('notificationEnvironment')"
              :rules="requiredRules"
              variant="outlined"
              density="compact"
              :disabled="destinationSaving"
            />
            <v-select
              v-if="['pagerduty', 'opsgenie'].includes(destinationForm.provider.trim())"
              v-model="destinationForm.region"
              :items="providerRegionOptions"
              item-title="text"
              item-value="value"
              :label="$t('notificationRegion')"
              :rules="requiredRules"
              variant="outlined"
              density="compact"
              :disabled="destinationSaving"
              data-testid="notification-destination-region"
            />
            <v-select
              v-if="destinationForm.provider.trim() === 'opsgenie'"
              v-model="destinationForm.opsgeniePriority"
              :items="opsgeniePriorityOptions"
              item-title="text"
              item-value="value"
              :label="$t('notificationOpsgeniePriority')"
              variant="outlined"
              density="compact"
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
              variant="outlined"
              density="compact"
              rows="3"
              :disabled="destinationSaving"
              data-testid="notification-destination-opsgenie-responders"
            />
            <template v-if="destinationForm.provider.trim() === 'servicenow'">
              <v-text-field
                v-model.trim="destinationForm.serviceNowInstanceOrigin"
                :label="$t('notificationServiceNowInstanceOrigin')"
                :hint="$t('notificationServiceNowInstanceOriginHint')"
                :rules="serviceNowOriginRules"
                persistent-hint
                variant="outlined"
                density="compact"
                :disabled="destinationSaving"
                data-testid="notification-destination-servicenow-origin"
              />
              <v-select
                v-model="destinationForm.serviceNowAuthMode"
                :items="serviceNowAuthModeOptions"
                item-title="text"
                item-value="value"
                :label="$t('notificationServiceNowAuthMode')"
                :rules="requiredRules"
                variant="outlined"
                density="compact"
                :disabled="destinationSaving"
                data-testid="notification-destination-servicenow-auth-mode"
              />
              <v-text-field
                v-if="destinationForm.serviceNowAuthMode === 'oauth_client_credentials'"
                v-model.trim="destinationForm.serviceNowClientID"
                :label="$t('notificationServiceNowClientID')"
                :rules="requiredRules"
                variant="outlined"
                density="compact"
                :disabled="destinationSaving"
                data-testid="notification-destination-servicenow-client-id"
              />
              <v-text-field
                v-else
                v-model.trim="destinationForm.serviceNowUsername"
                :label="$t('username')"
                :rules="requiredRules"
                variant="outlined"
                density="compact"
                :disabled="destinationSaving"
                data-testid="notification-destination-servicenow-username"
              />
              <v-text-field
                v-if="destinationForm.serviceNowAuthMode === 'oauth_client_credentials'"
                v-model.trim="destinationForm.serviceNowScope"
                :label="$t('notificationServiceNowScope')"
                :hint="$t('notificationServiceNowScopeHint')"
                persistent-hint
                variant="outlined"
                density="compact"
                :disabled="destinationSaving"
                data-testid="notification-destination-servicenow-scope"
              />
              <v-textarea
                v-model="destinationForm.serviceNowFieldMappingsText"
                :label="$t('notificationServiceNowFieldMappings')"
                :hint="$t('notificationServiceNowFieldMappingsHint')"
                :rules="serviceNowFieldMappingRules"
                persistent-hint
                variant="outlined"
                density="compact"
                rows="4"
                :disabled="destinationSaving"
                data-testid="notification-destination-servicenow-mappings"
              />
            </template>
            <v-text-field
              v-model="destinationForm.credential"
              :label="destinationCredentialLabel"
              :rules="destinationCredentialRules"
              type="password"
              autocomplete="new-password"
              :hint="$t('notificationCredentialPreserved')"
              persistent-hint
              variant="outlined"
              density="compact"
              :disabled="destinationSaving"
              data-testid="notification-destination-credential"
            />
            <v-checkbox
              v-model="destinationForm.enabled"
              :label="$t('enabled')"
              density="compact"
              :disabled="destinationSaving"
            />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="destinationSaving" @click="closeDestination">
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
              item-title="text"
              item-value="value"
              :label="$t('notificationDestination')"
              :rules="requiredRules"
              variant="outlined"
              density="compact"
              :disabled="ruleSaving"
            />
            <v-select
              v-model="ruleForm.source_kinds"
              :items="sourceKindOptions"
              :label="$t('notificationSources')"
              multiple
              variant="outlined"
              density="compact"
              :disabled="ruleSaving"
            />
            <v-select
              v-model="ruleForm.lifecycle_actions"
              :items="actionOptions"
              :label="$t('notificationActions')"
              multiple
              variant="outlined"
              density="compact"
              :disabled="ruleSaving"
            />
            <v-select
              v-model="ruleForm.minimum_severity"
              :items="severityOptions"
              :label="$t('notificationMinimumSeverity')"
              variant="outlined"
              density="compact"
              :disabled="ruleSaving"
            />
            <v-checkbox
              v-model="ruleForm.enabled"
              :label="$t('enabled')"
              density="compact"
              :disabled="ruleSaving"
            />
          </v-form>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="ruleSaving" @click="closeRule">{{ $t('cancel') }}</v-btn>
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

const SERVICE_NOW_DEFAULT_MAPPINGS = [
  'short_description=summary',
  'description=lifecycle_action',
  'impact=severity',
  'urgency=severity',
].join('\n');
const SERVICE_NOW_PRIORITY_FIELDS = ['impact', 'urgency'];
const SERVICE_NOW_DESCRIPTION_SOURCES = ['summary', 'lifecycle_action', 'status'];

const parseServiceNowFieldMappings = (value) => {
  const lines = value.split('\n').map((line) => line.trim()).filter(Boolean);
  if (lines.length === 0 || lines.length > 4) return null;
  const parsed = lines.reduce((state, line) => {
    if (state === null) return null;
    const match = /^(short_description|description|impact|urgency)=([a-z_]+)$/.exec(line);
    if (!match || state.targets.has(match[1])) return null;
    const [incidentField, sourceField] = match.slice(1);
    let compatible = false;
    if (incidentField === 'short_description') {
      compatible = sourceField === 'summary';
    } else if (SERVICE_NOW_PRIORITY_FIELDS.includes(incidentField)) {
      compatible = sourceField === 'severity';
    } else if (incidentField === 'description') {
      compatible = SERVICE_NOW_DESCRIPTION_SOURCES.includes(sourceField);
    }
    if (!compatible) return null;
    state.targets.add(incidentField);
    state.mappings.push({ incident_field: incidentField, source_field: sourceField });
    return state;
  }, { targets: new Set(), mappings: [] });
  return parsed && parsed.targets.has('short_description') ? parsed.mappings : null;
};

const formatServiceNowFieldMappings = (mappings = []) => mappings
  .map((mapping) => `${mapping.incident_field}=${mapping.source_field}`)
  .join('\n');

const isServiceNowHostname = (hostname) => {
  const lower = hostname.toLowerCase();
  const suffix = ['.service-now.com', '.servicenow.com']
    .find((candidate) => lower.endsWith(candidate));
  if (!suffix) return false;
  const prefix = lower.slice(0, -suffix.length);
  return prefix.length > 0 && prefix.split('.').every((label) => (
    /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label)
  ));
};

const isServiceNowOrigin = (value) => {
  try {
    const parsed = new URL(value);
    return parsed.protocol === 'https:'
      && parsed.username === ''
      && parsed.password === ''
      && (parsed.port === '' || parsed.port === '443')
      && parsed.pathname === '/'
      && parsed.search === ''
      && parsed.hash === ''
      && isServiceNowHostname(parsed.hostname);
  } catch (err) {
    return false;
  }
};

const newDestinationForm = () => ({
  id: null,
  revision: 0,
  name: '',
  provider: '',
  environment: '',
  region: 'us',
  opsgeniePriority: '',
  opsgenieRespondersText: '',
  serviceNowInstanceOrigin: '',
  serviceNowAuthMode: 'oauth_client_credentials',
  serviceNowClientID: '',
  serviceNowUsername: '',
  serviceNowScope: '',
  serviceNowFieldMappingsText: SERVICE_NOW_DEFAULT_MAPPINGS,
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
        { title: this.$t('name'), key: 'name', sortable: false },
        { title: this.$t('notificationProvider'), key: 'provider', sortable: false },
        { title: this.$t('notificationEnvironment'), key: 'environment', sortable: false },
        { title: this.$t('notificationCredential'), key: 'credential_configured', sortable: false },
        { title: this.$t('status'), key: 'state', sortable: false },
        {
          title: '', key: 'actions', sortable: false, align: 'end',
        },
      ];
    },
    ruleHeaders() {
      return [
        { title: this.$t('notificationDestination'), key: 'destination_id', sortable: false },
        { title: this.$t('notificationSources'), key: 'source_kinds', sortable: false },
        { title: this.$t('notificationActions'), key: 'lifecycle_actions', sortable: false },
        { title: this.$t('notificationMinimumSeverity'), key: 'minimum_severity', sortable: false },
        { title: this.$t('status'), key: 'enabled', sortable: false },
        {
          title: '', key: 'actions', sortable: false, align: 'end',
        },
      ];
    },
    historyHeaders() {
      return [
        { title: this.$t('notificationHistorySource'), key: 'source', sortable: false },
        { title: this.$t('notificationHistoryLifecycle'), key: 'lifecycle', sortable: false },
        { title: this.$t('notificationDestination'), key: 'destination_name', sortable: false },
        { title: this.$t('status'), key: 'status', sortable: false },
        { title: this.$t('attempts'), key: 'attempts', sortable: false },
        { title: this.$t('notificationHistoryReason'), key: 'last_reason', sortable: false },
        { title: this.$t('updated'), key: 'updated_at', sortable: false },
        {
          title: '', key: 'actions', sortable: false, align: 'end',
        },
      ];
    },
    eventHistoryHeaders() {
      return [
        { title: this.$t('notificationHistorySource'), key: 'source', sortable: false },
        { title: this.$t('notificationHistoryLifecycle'), key: 'lifecycle', sortable: false },
        { title: this.$t('notificationRoutingOutcome'), key: 'routing_outcome', sortable: false },
        { title: this.$t('notificationOccurred'), key: 'occurred_at', sortable: false },
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
    serviceNowAuthModeOptions() {
      return [
        {
          text: this.$t('notificationServiceNowAuthOAuthClientCredentials'),
          value: 'oauth_client_credentials',
        },
        { text: this.$t('notificationServiceNowAuthBasic'), value: 'basic' },
      ];
    },
    serviceNowOriginRules() {
      if (this.destinationForm.provider.trim() !== 'servicenow') return [];
      return [
        (value) => isServiceNowOrigin(value)
          || this.$t('notificationServiceNowOriginInvalid'),
      ];
    },
    serviceNowFieldMappingRules() {
      if (this.destinationForm.provider.trim() !== 'servicenow') return [];
      return [
        (value) => parseServiceNowFieldMappings(value) !== null
          || this.$t('notificationServiceNowMappingsInvalid'),
      ];
    },
    destinationCredentialLabel() {
      if (this.destinationForm.provider.trim() !== 'servicenow') {
        return this.$t('notificationCredential');
      }
      return this.destinationForm.serviceNowAuthMode === 'basic'
        ? this.$t('password')
        : this.$t('notificationServiceNowClientSecret');
    },
    destinationCredentialRules() {
      if (this.destinationForm.credential === '') return [];
      if (this.destinationForm.provider.trim() === 'pagerduty') {
        return [(value) => value.length === 32 || this.$t('notificationPagerDutyKeyLength')];
      }
      if (this.destinationForm.provider.trim() === 'opsgenie') {
        return [(value) => /^[\x21-\x7e]{1,256}$/.test(value) || this.$t('notificationOpsgenieKeyInvalid')];
      }
      if (this.destinationForm.provider.trim() === 'servicenow') {
        return [(value) => (value.length <= 1024 && value.trim() === value)
          || this.$t('notificationServiceNowCredentialInvalid')];
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
        serviceNowInstanceOrigin: destination.servicenow?.instance_origin || '',
        serviceNowAuthMode: destination.servicenow?.auth_mode || 'oauth_client_credentials',
        serviceNowClientID: destination.servicenow?.client_id || '',
        serviceNowUsername: destination.servicenow?.basic_username || '',
        serviceNowScope: destination.servicenow?.scope || '',
        serviceNowFieldMappingsText: formatServiceNowFieldMappings(
          destination.servicenow?.field_mappings,
        ) || SERVICE_NOW_DEFAULT_MAPPINGS,
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
      if (this.destinationSaving) return;
      if (this.$refs.destinationForm && !(await this.$refs.destinationForm.validate()).valid) {
        return;
      }
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
        if (this.destinationForm.provider.trim() === 'servicenow') {
          payload.servicenow = {
            instance_origin: this.destinationForm.serviceNowInstanceOrigin.trim(),
            auth_mode: this.destinationForm.serviceNowAuthMode,
            client_id: this.destinationForm.serviceNowAuthMode === 'oauth_client_credentials'
              ? this.destinationForm.serviceNowClientID.trim() : '',
            basic_username: this.destinationForm.serviceNowAuthMode === 'basic'
              ? this.destinationForm.serviceNowUsername.trim() : '',
            scope: this.destinationForm.serviceNowAuthMode === 'oauth_client_credentials'
              ? this.destinationForm.serviceNowScope.trim() : '',
            field_mappings: parseServiceNowFieldMappings(
              this.destinationForm.serviceNowFieldMappingsText,
            ),
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
      if (this.ruleSaving) return;
      if (this.$refs.ruleForm && !(await this.$refs.ruleForm.validate()).valid) return;
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
    serviceNowAuthModeLabel(mode) {
      if (mode === 'oauth_client_credentials') {
        return this.$t('notificationServiceNowAuthOAuthClientCredentials');
      }
      if (mode === 'basic') return this.$t('notificationServiceNowAuthBasic');
      return '—';
    },
    safeProviderRecordURL(item) {
      if (item.destination_provider !== 'servicenow'
        || !/^[a-f0-9]{32}$/.test(item.provider_record_id || '')) return '';
      try {
        const parsed = new URL(item.provider_record_url || '');
        const keys = [...parsed.searchParams.keys()];
        if (parsed.protocol !== 'https:'
          || parsed.username !== ''
          || parsed.password !== ''
          || (parsed.port !== '' && parsed.port !== '443')
          || !isServiceNowHostname(parsed.hostname)
          || parsed.pathname !== '/incident.do'
          || parsed.hash !== ''
          || keys.length !== 1
          || keys[0] !== 'sys_id'
          || parsed.searchParams.get('sys_id') !== item.provider_record_id) return '';
        return parsed.toString();
      } catch (err) {
        return '';
      }
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
