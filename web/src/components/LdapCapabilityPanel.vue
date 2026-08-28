<template>
  <v-card data-testid="ldap-capability" style="background: var(--highlighted-card-bg-color)">
    <v-card-text>
      <v-alert v-if="error" data-testid="ldap-error" type="error" dense outlined>
        {{ error }}
      </v-alert>
      <div class="ldap-toolbar mb-4">
        <v-select
          v-model="selectedProviderID"
          data-testid="ldap-provider-select"
          :items="providers"
          item-text="display_name"
          item-value="id"
          label="LDAP provider"
          hide-details
          outlined
          dense
          @change="selectProvider"
        />
        <v-btn data-testid="ldap-provider-new" outlined @click="newProvider">
          New provider
        </v-btn>
      </div>

      <v-form ref="configurationForm">
        <v-row dense>
          <v-col cols="12" md="4">
            <v-text-field
              v-model="form.id"
              data-testid="ldap-provider-id"
              label="Provider ID"
              :disabled="providerExists"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="8">
            <v-text-field v-model="form.display_name" label="Display name" outlined dense />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.server_url"
              label="Server URL"
              placeholder="ldaps://ldap.example.com:636"
              persistent-hint
              hint="Only LDAPS or LDAP upgraded with StartTLS is accepted."
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-select v-model="form.tls_mode" :items="tlsModes" label="TLS mode" outlined dense />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-select v-model="form.trust_mode" :items="trustModes" label="Trust" outlined dense />
          </v-col>
          <v-col v-if="form.trust_mode === 'custom_ca'" cols="12">
            <v-textarea
              v-model="form.ca_pem"
              label="Custom CA certificate (PEM)"
              rows="4"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field v-model="form.bind_dn" label="Bind DN" outlined dense />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="bindPassword"
              data-testid="ldap-bind-password"
              name="bind_password"
              type="password"
              autocomplete="new-password"
              :label="form.bind_password_configured
                ? 'Replace bind password (leave blank to keep it)'
                : 'Bind password'"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.search_base_dn"
              label="Fixed user search base"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.user_filter"
              label="User filter"
              :placeholder="filterPlaceholder"
              persistent-hint
              :hint="filterHint"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-select
              v-model="form.identity_attribute"
              :items="identityAttributes"
              label="Immutable identity"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-text-field
              v-model="form.username_attribute"
              label="Username attribute"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-text-field v-model="form.name_attribute" label="Name attribute" outlined dense />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-text-field v-model="form.email_attribute" label="Email attribute" outlined dense />
          </v-col>
        </v-row>
      </v-form>
      <v-alert
        v-if="configurationLocked"
        data-testid="ldap-configuration-locked"
        type="info"
        dense
        outlined
      >
        Move this provider to Shadow or Disabled before changing its connection configuration.
      </v-alert>
      <v-btn
        data-testid="ldap-save"
        color="primary"
        :loading="saving"
        :disabled="!form.id || configurationLocked"
        @click="save"
      >
        Save configuration
      </v-btn>

      <template v-if="providerExists">
        <v-divider class="my-6" />
        <h3 class="subtitle-1 mb-3">Readiness and local recovery</h3>
        <v-alert
          v-if="readiness"
          data-testid="ldap-readiness"
          :type="readiness.status === 'ready' ? 'success' : 'warning'"
          dense
          outlined
        >
          <strong>{{ readiness.status }}</strong>
          <span v-if="readiness.checked_at"> · {{ formatTime(readiness.checked_at) }}</span>
          <div class="mt-1 readiness-grid">
            <span>Connection: {{ readiness.connection ? 'passed' : 'not passed' }}</span>
            <span>Search: {{ readiness.search ? 'passed' : 'not passed' }}</span>
            <span>User bind: {{ readiness.bind ? 'passed' : 'not passed' }}</span>
            <span>Local recovery: {{ readiness.recovery ? 'passed' : 'not passed' }}</span>
          </div>
        </v-alert>
        <v-alert type="info" dense outlined>
          Enabling directory login requires a successful directory login and a password check for
          a local administrator. Readiness expires after 15 minutes; that administrator remains
          the recovery login afterward.
        </v-alert>
        <v-row dense>
          <v-col cols="12" md="6">
            <v-text-field v-model="testUsername" label="Directory test username" outlined dense />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="testPassword"
              type="password"
              autocomplete="off"
              label="Directory test password"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-select
              v-model="recoveryAdminUserID"
              :items="localAdmins"
              item-text="username"
              item-value="id"
              label="Local recovery administrator"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="recoveryAdminPassword"
              type="password"
              autocomplete="off"
              label="Local administrator password"
              outlined
              dense
            />
          </v-col>
        </v-row>
        <v-btn
          data-testid="ldap-test"
          outlined
          :loading="testLoading"
          :disabled="!testUsername
            || !testPassword
            || !recoveryAdminUserID
            || !recoveryAdminPassword"
          @click="testConnection"
        >
          Test directory and recovery
        </v-btn>

        <v-divider class="my-6" />
        <h3 class="subtitle-1 mb-3">Login lifecycle</h3>
        <v-select
          v-model="form.state"
          data-testid="ldap-state"
          :items="states"
          label="Lifecycle state"
          outlined
          dense
        />
        <v-select
          v-if="form.state === 'selected_users'"
          v-model="form.selected_user_ids"
          data-testid="ldap-selected-users"
          :items="eligibleUsers"
          item-text="username"
          item-value="id"
          label="Users with an already linked identity"
          multiple
          chips
          persistent-hint
          hint="Link the LDAP identity from the user profile before selecting the account here."
          outlined
          dense
        />
        <v-alert v-if="form.state === 'shadow'" type="info" dense outlined>
          Shadow mode retains configuration and readiness but does not expose directory login.
        </v-alert>
        <v-alert
          v-if="form.state === 'selected_users' || form.state === 'active'"
          type="warning"
          dense
          outlined
        >
          Directory login will be exposed. Verify local recovery immediately before applying.
        </v-alert>
        <v-btn
          data-testid="ldap-apply-state"
          color="primary"
          :loading="stateSaving"
          @click="applyState"
        >
          Apply lifecycle state
        </v-btn>
      </template>
    </v-card-text>
  </v-card>
</template>

<script>
import axios from 'axios';

function emptyProvider() {
  return {
    id: '',
    display_name: '',
    state: 'disabled',
    server_url: '',
    tls_mode: 'ldaps',
    trust_mode: 'system',
    ca_pem: '',
    bind_dn: '',
    bind_password_configured: false,
    search_base_dn: '',
    user_filter: '(uid={{username}})',
    identity_attribute: 'entryUUID',
    username_attribute: 'uid',
    name_attribute: 'cn',
    email_attribute: 'mail',
    selected_user_ids: [],
    eligible_user_ids: [],
    readiness: { status: 'untested' },
  };
}

export default {
  data() {
    return {
      providers: [],
      users: [],
      selectedProviderID: '',
      form: emptyProvider(),
      bindPassword: '',
      testUsername: '',
      testPassword: '',
      recoveryAdminUserID: null,
      recoveryAdminPassword: '',
      readiness: null,
      loading: false,
      saving: false,
      testLoading: false,
      stateSaving: false,
      error: '',
      filterPlaceholder: '(uid={{username}})',
      filterHint: 'Include {{username}} exactly once; the supplied username is escaped.',
      tlsModes: [
        { text: 'LDAPS', value: 'ldaps' },
        { text: 'StartTLS', value: 'starttls' },
      ],
      trustModes: [
        { text: 'System trust store', value: 'system' },
        { text: 'Custom CA', value: 'custom_ca' },
      ],
      identityAttributes: ['entryUUID', 'objectGUID', 'nsUniqueId', 'ipaUniqueID'],
      states: [
        { text: 'Disabled', value: 'disabled' },
        { text: 'Shadow', value: 'shadow' },
        { text: 'Selected users', value: 'selected_users' },
        { text: 'Active for everyone', value: 'active' },
      ],
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
  },

  created() {
    this.load();
  },

  methods: {
    emptyProvider,

    async load() {
      this.loading = true;
      this.error = '';
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
        this.error = this.errorMessage(error);
      } finally {
        this.loading = false;
      }
    },

    selectProvider() {
      const provider = this.providers.find((entry) => entry.id === this.selectedProviderID);
      if (provider) this.applyProvider(provider);
    },

    applyProvider(provider) {
      const sanitized = { ...provider };
      delete sanitized.bind_password;
      this.form = { ...emptyProvider(), ...sanitized };
      this.bindPassword = '';
      this.readiness = provider.readiness || { status: 'untested' };
      this.recoveryAdminUserID = provider.recovery_admin_user_id || null;
    },

    newProvider() {
      this.selectedProviderID = '';
      this.form = emptyProvider();
      this.bindPassword = '';
      this.readiness = null;
      this.recoveryAdminUserID = null;
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
      };
    },

    async save() {
      this.saving = true;
      this.error = '';
      try {
        const response = await axios.put('/api/capabilities/ldap', this.configurationPayload());
        const index = this.providers.findIndex((provider) => provider.id === response.data.id);
        if (index === -1) this.providers.push(response.data);
        else this.providers.splice(index, 1, response.data);
        this.selectedProviderID = response.data.id;
        this.applyProvider(response.data);
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.bindPassword = '';
        this.saving = false;
      }
    },

    async testConnection() {
      this.testLoading = true;
      this.error = '';
      const proof = {
        provider_id: this.selectedProviderID,
        username: this.testUsername,
        recovery_admin_user_id: this.recoveryAdminUserID,
      };
      proof.password = this.testPassword;
      proof.recovery_admin_password = this.recoveryAdminPassword;
      try {
        const response = await axios.post('/api/capabilities/ldap/test', proof);
        this.readiness = response.data;
        await this.load();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.testPassword = '';
        this.recoveryAdminPassword = '';
        this.testLoading = false;
      }
    },

    async applyState() {
      this.stateSaving = true;
      this.error = '';
      try {
        const response = await axios.put('/api/capabilities/ldap/state', {
          provider_id: this.selectedProviderID,
          state: this.form.state,
          selected_user_ids: this.form.state === 'selected_users'
            ? this.form.selected_user_ids : [],
        });
        const index = this.providers.findIndex((provider) => provider.id === response.data.id);
        if (index !== -1) this.providers.splice(index, 1, response.data);
        this.applyProvider(response.data);
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.stateSaving = false;
      }
    },

    errorMessage(error) {
      const code = error.response?.data?.error;
      if (code === 'LDAP_ADMIN_RECOVERY_NOT_READY') {
        return 'Directory login was not enabled because directory or local recovery readiness is missing or expired.';
      }
      if (code === 'LDAP_PROVIDER_UNAVAILABLE') {
        return 'The LDAP provider could not be reached securely. Existing local login remains available.';
      }
      if (code === 'LDAP_INVALID_CREDENTIALS') {
        return 'The directory credentials were rejected. Check the test username and password.';
      }
      if (code === 'LDAP_RECONFIGURATION_REQUIRES_INACTIVE') {
        return 'Move this provider to Shadow or Disabled before changing its connection configuration.';
      }
      return code || error.message || 'LDAP operation failed.';
    },

    formatTime(value) {
      const parsed = new Date(value);
      return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
    },
  },
};
</script>

<style scoped>
.ldap-toolbar {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  align-items: start;
}
.readiness-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 12px;
}
@media (max-width: 600px) {
  .ldap-toolbar { grid-template-columns: minmax(0, 1fr); }
  .readiness-grid { grid-template-columns: minmax(0, 1fr); }
}
</style>
