<template>
  <div data-testid="ldap-capability" class="ldap-setup">
    <v-alert v-if="loadError" type="error" variant="outlined" density="compact" class="mb-4">
      {{ loadError }}
    </v-alert>

    <div class="ldap-setup__toolbar">
      <v-select
        v-model="selectedProviderID"
        data-testid="ldap-provider-select"
        :items="providers"
        item-title="display_name"
        item-value="id"
        :label="$t('ldapProvider')"
        :placeholder="$t('ldapNoProvider')"
        hide-details
        variant="outlined"
        density="compact"
        @update:model-value="selectProvider"
      />
      <v-btn data-testid="ldap-provider-new" variant="outlined" @click="newProvider">
        {{ $t('ldapNewProvider') }}
      </v-btn>
    </div>
    <p v-if="!providerExists" class="text-body-medium mb-4" data-testid="ldap-new-provider-hint">
      {{ $t('ldapNewProviderHint') }}
    </p>

    <!-- 1 Connection -->
    <section class="ldap-step" data-testid="ldap-step-connection">
      <h3
        class="ldap-step__title">
        <span class="ldap-step__number">1</span>{{ $t('ldapStepConnection') }}
      </h3>
      <div class="ldap-step__presets">
        <span class="text-body-medium">{{ $t('ldapPresetIntro') }}</span>
        <v-btn
          size="small"
          variant="tonal"
          :disabled="configurationLocked"
          data-testid="ldap-preset-ad"
          @click="usePreset('active_directory')"
        >Active Directory</v-btn>
        <v-btn
          size="small"
          variant="tonal"
          :disabled="configurationLocked"
          data-testid="ldap-preset-openldap"
          @click="usePreset('openldap')"
        >OpenLDAP</v-btn>
        <span v-if="presetApplied" class="text-success text-body-medium" role="status">
          {{ $t('ldapPresetApplied', { preset: presetApplied }) }}
        </span>
      </div>
      <v-row density="compact">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.display_name"
            data-testid="ldap-display-name"
            :label="$t('ldapDisplayName')"
            :hint="$t('ldapDisplayNameHint')"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
            @update:model-value="displayNameChanged"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.id"
            data-testid="ldap-provider-id"
            :label="$t('ldapProviderId')"
            :hint="providerExists ? $t('ldapProviderIdFixed') : $t('ldapProviderIdHint')"
            persistent-hint
            :disabled="providerExists"
            variant="outlined"
            density="compact"
            @update:model-value="providerIdEdited = true"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.server_url"
            data-testid="ldap-server-url"
            :label="$t('ldapServerUrl')"
            placeholder="ldaps://ldap.example.com:636"
            :hint="$t('ldapServerUrlHint')"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="3">
          <v-select
            v-model="form.tls_mode"
            :items="tlsModes"
            item-title="text"
            :label="$t('ldapTlsMode')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="3">
          <v-select
            v-model="form.trust_mode"
            :items="trustModes"
            item-title="text"
            :label="$t('ldapTrust')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col v-if="form.trust_mode === 'custom_ca'" cols="12">
          <v-textarea
            v-model="form.ca_pem"
            data-testid="ldap-ca-pem"
            :label="$t('ldapCaPem')"
            :hint="$t('ldapCaPemHint')"
            persistent-hint
            rows="4"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.bind_dn"
            data-testid="ldap-bind-dn"
            :label="$t('ldapBindDn')"
            :hint="$t('ldapBindDnHint')"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="bindPassword"
            data-testid="ldap-bind-password"
            name="bind_password"
            type="password"
            autocomplete="new-password"
            :label="form.bind_password_configured
              ? $t('ldapBindPasswordReplace') : $t('ldapBindPassword')"
            :hint="form.bind_password_configured ? $t('ldapBindPasswordStored') : ''"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
      </v-row>
    </section>

    <!-- 2 People -->
    <section class="ldap-step" data-testid="ldap-step-people">
      <h3
        class="ldap-step__title">
        <span class="ldap-step__number">2</span>{{ $t('ldapStepPeople') }}
      </h3>
      <v-row density="compact">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.search_base_dn"
            data-testid="ldap-search-base"
            :label="$t('ldapUserSearchBase')"
            :hint="$t('ldapUserSearchBaseHint')"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.user_filter"
            data-testid="ldap-user-filter"
            :label="$t('ldapUserFilter')"
            :hint="$t('ldapUserFilterHint', { placeholder: '{{username}}' })"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="3">
          <v-select
            v-model="form.identity_attribute"
            :items="identityAttributes"
            :label="$t('ldapIdentityAttribute')"
            :hint="$t('ldapIdentityAttributeHint')"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model="form.username_attribute"
            :label="$t('ldapUsernameAttribute')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model="form.name_attribute"
            :label="$t('ldapNameAttribute')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="3">
          <v-text-field
            v-model="form.email_attribute"
            :label="$t('ldapEmailAttribute')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
      </v-row>
    </section>

    <!-- 3 Groups -->
    <section class="ldap-step" data-testid="ldap-step-groups">
      <h3
        class="ldap-step__title">
        <span class="ldap-step__number">3</span>{{ $t('ldapStepGroups') }}
      </h3>
      <p class="text-body-medium mb-3">{{ $t('ldapStepGroupsIntro') }}</p>
      <v-row density="compact">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.group_search_base_dn"
            data-testid="ldap-group-search-base"
            :label="$t('ldapGroupSearchBase')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.group_filter"
            :label="$t('ldapGroupFilter')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="form.group_user_filter"
            :label="$t('ldapGroupUserFilter')"
            :hint="$t('ldapGroupUserFilterHint')"
            persistent-hint
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="2">
          <v-select
            v-model="form.group_identity_attribute"
            :items="identityAttributes"
            :label="$t('ldapGroupIdentityAttribute')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="2">
          <v-text-field
            v-model="form.group_member_attribute"
            :label="$t('ldapGroupMemberAttribute')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="2">
          <v-text-field
            v-model="form.group_name_attribute"
            :label="$t('ldapGroupNameAttribute')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" sm="6" md="2">
          <v-text-field
            v-model.number="form.group_max_depth"
            type="number"
            min="1"
            max="16"
            :label="$t('ldapGroupMaxDepth')"
            :disabled="configurationLocked"
            variant="outlined"
            density="compact"
          />
        </v-col>
      </v-row>

      <div class="ldap-step__actions">
        <v-btn
          data-testid="ldap-save"
          color="primary"
          :loading="saving"
          :disabled="!form.id || configurationLocked"
          @click="save"
        >{{ $t('ldapSave') }}</v-btn>
        <span v-if="savedAt" class="text-success" role="status" data-testid="ldap-saved">
          {{ $t('ldapSaved') }}
        </span>
      </div>
      <v-alert
        v-if="configurationLocked"
        data-testid="ldap-configuration-locked"
        type="info"
        density="compact"
        variant="outlined"
        class="mt-3"
      >{{ $t('ldapConfigurationLocked') }}</v-alert>
      <v-alert
        v-if="saveError"
        data-testid="ldap-save-error"
        type="error"
        density="compact"
        variant="outlined"
        role="alert"
        class="mt-3"
      >{{ saveError }}</v-alert>
    </section>

    <!-- 4 Test -->
    <section class="ldap-step" data-testid="ldap-step-test">
      <h3
        class="ldap-step__title">
        <span class="ldap-step__number">4</span>{{ $t('ldapStepTest') }}
      </h3>
      <p class="text-body-medium mb-3">{{ $t('ldapStepTestIntro') }}</p>
      <v-row density="compact">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="testUsername"
            data-testid="ldap-test-username"
            :label="$t('ldapTestUsername')"
            :disabled="!providerExists"
            autocomplete="off"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="testPassword"
            data-testid="ldap-test-password"
            type="password"
            autocomplete="off"
            :label="$t('ldapTestPassword')"
            :disabled="!providerExists"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-select
            v-model="recoveryAdminUserID"
            data-testid="ldap-recovery-admin"
            :items="localAdmins"
            item-title="username"
            item-value="id"
            :label="$t('ldapRecoveryAdmin')"
            :hint="$t('ldapRecoveryAdminHint')"
            persistent-hint
            :disabled="!providerExists"
            variant="outlined"
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="recoveryAdminPassword"
            data-testid="ldap-recovery-password"
            type="password"
            autocomplete="off"
            :label="$t('ldapRecoveryPassword')"
            :disabled="!providerExists"
            variant="outlined"
            density="compact"
          />
        </v-col>
      </v-row>
      <div class="ldap-step__actions">
        <v-btn
          data-testid="ldap-test"
          variant="outlined"
          color="primary"
          :loading="testLoading"
          :disabled="!providerExists || !testUsername || !testPassword
            || !recoveryAdminUserID || !recoveryAdminPassword"
          @click="testConnection"
        >{{ $t('ldapRunTest') }}</v-btn>
      </div>

      <div
        v-if="readiness"
        class="ldap-readiness mt-4"
        data-testid="ldap-readiness"
        role="status"
        aria-live="polite"
      >
        <div class="ldap-readiness__stages">
          <div
            v-for="stage in stageStates"
            :key="stage.key"
            class="ldap-readiness__stage"
            :data-testid="`ldap-stage-${stage.key}`"
            :data-state="stage.state"
          >
            <v-icon
              size="small"
              :color="stageColor(stage.state)">
              {{ stageIcon(stage.state) }}
            </v-icon>
            <span>{{ $t(stage.label) }}</span>
          </div>
        </div>
        <v-alert
          :type="readinessAlertType"
          variant="tonal"
          density="compact"
          class="mt-2"
          data-testid="ldap-readiness-message"
        >
          <div>{{ readinessText }}</div>
          <div
            v-if="readiness.detail"
            class="ldap-readiness__detail"
            data-testid="ldap-readiness-detail">
            {{ readiness.detail }}
          </div>
          <div v-if="readiness.checked_at" class="text-body-small mt-1">
            {{ $t('ldapCheckedAt', { time: formatTime(readiness.checked_at) }) }}
            <template v-if="expiry">
              · {{ $t('ldapValidUntil', { time: formatTime(expiry) }) }}
            </template>
          </div>
        </v-alert>
      </div>
      <v-alert
        v-if="testError"
        data-testid="ldap-test-error"
        type="error"
        density="compact"
        variant="outlined"
        role="alert"
        class="mt-3"
      >{{ testError }}</v-alert>
    </section>

    <!-- 5 Turn on -->
    <section class="ldap-step" data-testid="ldap-step-activate">
      <h3
        class="ldap-step__title">
        <span class="ldap-step__number">5</span>{{ $t('ldapStepActivate') }}
      </h3>
      <v-radio-group
        v-model="form.state"
        data-testid="ldap-state"
        :disabled="!providerExists"
        hide-details
      >
        <v-radio
          v-for="option in states"
          :key="option.value"
          :value="option.value"
          :data-testid="`ldap-state-${option.value}`"
        >
          <template v-slot:label>
            <div>
              <div class="font-weight-medium">{{ $t(option.label) }}</div>
              <div class="text-body-small text-medium-emphasis">{{ $t(option.description) }}</div>
            </div>
          </template>
        </v-radio>
      </v-radio-group>
      <v-autocomplete
        v-if="form.state === 'selected_users'"
        v-model="form.selected_user_ids"
        data-testid="ldap-selected-users"
        :items="eligibleUsers"
        item-title="username"
        item-value="id"
        :label="$t('ldapSelectedUsers')"
        :hint="$t('ldapSelectedUsersHint')"
        persistent-hint
        multiple
        chips
        closable-chips
        variant="outlined"
        density="compact"
        class="mt-2"
      />
      <p class="text-body-medium mt-3">
        {{ $t('ldapActivateRequirements') }}
        <router-link to="/admin/group-mappings">{{ $t('adminGroupMappings') }}</router-link>
      </p>
      <div class="ldap-step__actions">
        <v-btn
          data-testid="ldap-apply-state"
          color="primary"
          :loading="stateSaving"
          :disabled="!providerExists || form.state === (selectedProvider && selectedProvider.state)
            && form.state !== 'selected_users'"
          @click="applyState"
        >{{ $t('ldapApplyState') }}</v-btn>
        <span v-if="stateSaved" class="text-success" role="status" data-testid="ldap-state-saved">
          {{ $t('ldapStateSaved', { state: appliedStateLabel }) }}
        </span>
      </div>
      <v-alert
        v-if="stateError"
        data-testid="ldap-state-error"
        type="error"
        density="compact"
        variant="outlined"
        role="alert"
        class="mt-3"
      >
        {{ stateError }}
        <div v-if="stateErrorNeedsMapping" class="mt-1">
          <router-link to="/admin/group-mappings">{{ $t('ldapOpenGroupMappings') }}</router-link>
        </div>
      </v-alert>
    </section>
  </div>
</template>

<script>
import axios from 'axios';
import {
  applyLdapPreset, emptyLdapProvider, ldapErrorText, ldapStageStates,
  LDAP_READINESS_MESSAGES, mappingIdFromName, readinessExpiry,
} from '@/lib/enhanced/ldap';

export const LDAP_STATES = [
  { value: 'disabled', label: 'ldapStateDisabled', description: 'ldapStateDisabledHint' },
  { value: 'shadow', label: 'ldapStateShadow', description: 'ldapStateShadowHint' },
  { value: 'selected_users', label: 'ldapStateSelected', description: 'ldapStateSelectedHint' },
  { value: 'active', label: 'ldapStateActive', description: 'ldapStateActiveHint' },
];

export default {
  data() {
    return {
      providers: [],
      users: [],
      selectedProviderID: '',
      form: emptyLdapProvider(),
      bindPassword: '',
      providerIdEdited: false,
      presetApplied: '',
      testUsername: '',
      testPassword: '',
      recoveryAdminUserID: null,
      recoveryAdminPassword: '',
      readiness: null,
      saving: false,
      savedAt: null,
      testLoading: false,
      stateSaving: false,
      stateSaved: false,
      loadError: '',
      saveError: '',
      testError: '',
      stateError: '',
      stateErrorNeedsMapping: false,
      states: LDAP_STATES,
      tlsModes: [
        { text: 'LDAPS', value: 'ldaps' },
        { text: 'StartTLS', value: 'starttls' },
      ],
      trustModes: [
        { text: 'System trust store', value: 'system' },
        { text: 'Custom CA', value: 'custom_ca' },
      ],
      identityAttributes: ['entryUUID', 'objectGUID', 'nsUniqueId', 'ipaUniqueID'],
    };
  },

  computed: {
    selectedProvider() {
      return this.providers.find((provider) => provider.id === this.selectedProviderID) || null;
    },
    providerExists() {
      return Boolean(this.selectedProvider);
    },
    configurationLocked() {
      return ['active', 'selected_users'].includes(this.selectedProvider?.state);
    },
    localAdmins() {
      return this.users.filter((user) => user.admin && !user.external);
    },
    eligibleUsers() {
      const eligible = new Set(this.form.eligible_user_ids || []);
      return this.users.filter((user) => eligible.has(user.id));
    },
    stageStates() {
      return ldapStageStates(this.readiness);
    },
    readinessText() {
      const key = LDAP_READINESS_MESSAGES[this.readiness?.code]
        || (this.readiness?.status === 'ready' ? 'ldapReadinessReady' : 'ldapReadinessUntested');
      return this.$t(key);
    },
    expiry() {
      return readinessExpiry(this.readiness);
    },
    readinessAlertType() {
      return { ready: 'success', failed: 'error' }[this.readiness?.status] || 'info';
    },
    appliedStateLabel() {
      return this.stateLabel(this.selectedProvider && this.selectedProvider.state);
    },
  },

  created() {
    this.load();
  },

  methods: {
    async load() {
      this.loadError = '';
      try {
        const [providers, users] = await Promise.all([
          axios.get('/api/capabilities/ldap'),
          axios.get('/api/users'),
        ]);
        this.providers = providers.data || [];
        this.users = users.data || [];
        if (!this.selectedProviderID && this.providers.length) {
          this.selectedProviderID = this.providers[0].id;
        }
        this.selectProvider();
      } catch (error) {
        this.loadError = ldapErrorText(error, this.$t);
      }
    },

    selectProvider() {
      const provider = this.selectedProvider;
      if (provider) this.applyProvider(provider);
      this.clearFeedback();
    },

    applyProvider(provider) {
      const sanitized = { ...provider };
      delete sanitized.bind_password;
      this.form = { ...emptyLdapProvider(), ...sanitized };
      this.bindPassword = '';
      this.readiness = provider.readiness || { status: 'untested' };
      this.recoveryAdminUserID = provider.recovery_admin_user_id || this.recoveryAdminUserID;
    },

    clearFeedback() {
      this.saveError = '';
      this.testError = '';
      this.stateError = '';
      this.stateErrorNeedsMapping = false;
      this.savedAt = null;
      this.stateSaved = false;
      this.presetApplied = '';
    },

    newProvider() {
      this.selectedProviderID = '';
      this.form = emptyLdapProvider();
      this.bindPassword = '';
      this.providerIdEdited = false;
      this.readiness = null;
      this.clearFeedback();
    },

    displayNameChanged(name) {
      if (!this.providerExists && !this.providerIdEdited) {
        this.form.id = mappingIdFromName(name, this.providers.map((provider) => provider.id));
      }
    },

    usePreset(preset) {
      this.form = applyLdapPreset(this.form, preset);
      this.presetApplied = preset === 'active_directory' ? 'Active Directory' : 'OpenLDAP';
    },

    configurationPayload() {
      return {
        id: this.form.id,
        display_name: this.form.display_name,
        server_url: this.form.server_url,
        tls_mode: this.form.tls_mode,
        trust_mode: this.form.trust_mode,
        ca_pem: this.form.ca_pem,
        bind_dn: this.form.bind_dn,
        bind_password: this.bindPassword,
        search_base_dn: this.form.search_base_dn,
        user_filter: this.form.user_filter,
        identity_attribute: this.form.identity_attribute,
        username_attribute: this.form.username_attribute,
        name_attribute: this.form.name_attribute,
        email_attribute: this.form.email_attribute,
        group_search_base_dn: this.form.group_search_base_dn,
        group_user_filter: this.form.group_user_filter,
        group_filter: this.form.group_filter,
        group_identity_attribute: this.form.group_identity_attribute,
        group_member_attribute: this.form.group_member_attribute,
        group_name_attribute: this.form.group_name_attribute,
        group_max_depth: this.form.group_max_depth,
      };
    },

    upsertProvider(provider) {
      const index = this.providers.findIndex((entry) => entry.id === provider.id);
      if (index === -1) this.providers.push(provider);
      else this.providers.splice(index, 1, provider);
      this.selectedProviderID = provider.id;
      this.applyProvider(provider);
    },

    async save() {
      this.saving = true;
      this.clearFeedback();
      try {
        const response = await axios.put('/api/capabilities/ldap', this.configurationPayload());
        this.upsertProvider(response.data);
        this.savedAt = new Date();
      } catch (error) {
        this.saveError = ldapErrorText(error, this.$t);
      } finally {
        this.bindPassword = '';
        this.saving = false;
      }
    },

    async testConnection() {
      this.testLoading = true;
      this.testError = '';
      try {
        const response = await axios.post('/api/capabilities/ldap/test', {
          provider_id: this.selectedProviderID,
          username: this.testUsername,
          password: this.testPassword,
          recovery_admin_user_id: this.recoveryAdminUserID,
          recovery_admin_password: this.recoveryAdminPassword,
        });
        this.readiness = response.data;
      } catch (error) {
        const body = error.response?.data;
        if (body && body.status) this.readiness = body;
        else this.testError = ldapErrorText(error, this.$t);
      } finally {
        this.testPassword = '';
        this.recoveryAdminPassword = '';
        this.testLoading = false;
        await this.refreshReadiness();
      }
    },

    async refreshReadiness() {
      try {
        const providers = (await axios.get('/api/capabilities/ldap')).data || [];
        this.providers = providers;
        const provider = this.selectedProvider;
        if (provider?.readiness) this.readiness = provider.readiness;
      } catch {
        // The test result shown above stays; a reload error is not the test outcome.
      }
    },

    async applyState() {
      this.stateSaving = true;
      this.stateError = '';
      this.stateErrorNeedsMapping = false;
      this.stateSaved = false;
      try {
        const response = await axios.put('/api/capabilities/ldap/state', {
          provider_id: this.selectedProviderID,
          state: this.form.state,
          selected_user_ids: this.form.state === 'selected_users' ? this.form.selected_user_ids : [],
        });
        this.upsertProvider(response.data);
        this.stateSaved = true;
      } catch (error) {
        this.stateError = ldapErrorText(error, this.$t);
        this.stateErrorNeedsMapping = error.response?.data?.error === 'LDAP_NO_GROUP_MAPPING';
      } finally {
        this.stateSaving = false;
      }
    },

    stateLabel(value) {
      const state = LDAP_STATES.find((option) => option.value === value);
      return state ? this.$t(state.label) : value;
    },

    stageIcon(state) {
      return {
        passed: 'mdi-check-circle', failed: 'mdi-close-circle', not_reached: 'mdi-minus-circle-outline',
      }[state] || 'mdi-circle-outline';
    },

    stageColor(state) {
      return { passed: 'success', failed: 'error' }[state] || 'grey';
    },

    formatTime(value) {
      const parsed = new Date(value);
      return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
    },
  },
};
</script>

<style scoped>
.ldap-setup__toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: center;
  margin-bottom: 12px;
}
.ldap-step {
  padding: 16px 0 8px;
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.ldap-step__title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 1.05rem;
  font-weight: 600;
  margin-bottom: 12px;
}
.ldap-step__number {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border-radius: 50%;
  background: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-on-primary));
  font-size: 0.85rem;
}
.ldap-step__presets,
.ldap-step__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
  margin-bottom: 12px;
}
.ldap-step__actions {
  margin-top: 12px;
}
.ldap-readiness__stages {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 20px;
}
.ldap-readiness__stage {
  display: flex;
  align-items: center;
  gap: 6px;
}
.ldap-readiness__detail {
  font-family: monospace;
  overflow-wrap: anywhere;
  margin-top: 4px;
}
@media (max-width: 600px) {
  .ldap-setup__toolbar {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
