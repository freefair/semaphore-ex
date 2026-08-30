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
          <v-col cols="12">
            <v-divider class="mb-4" />
            <div class="subtitle-2 mb-2">Group discovery schema</div>
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.group_search_base_dn"
              data-testid="ldap-group-search-base"
              label="Fixed group search base"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.group_user_filter"
              label="User listing filter"
              hint="Static filter without username markers."
              persistent-hint
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              v-model="form.group_filter"
              label="Group listing filter"
              hint="Static filter used with paged directory search."
              persistent-hint
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-select
              v-model="form.group_identity_attribute"
              :items="identityAttributes"
              label="Immutable group identity"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-text-field
              v-model="form.group_member_attribute"
              label="Group member attribute"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="6" md="3">
            <v-text-field
              v-model.number="form.group_max_depth"
              type="number"
              min="1"
              max="16"
              label="Nested group depth"
              outlined
              dense
            />
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

        <v-divider class="my-6" />
        <div class="d-flex align-center flex-wrap mb-3 ldap-section-heading">
          <h3 class="subtitle-1">Group-to-role mappings</h3>
          <v-spacer />
          <v-btn
            data-testid="ldap-group-preview"
            outlined
            small
            :loading="groupPreviewLoading"
            @click="previewGroupMappings"
          >
            Preview changes
          </v-btn>
          <v-btn
            class="ml-2"
            outlined
            small
            :loading="groupReconcileLoading"
            @click="reconcileGroupMappings"
          >
            Reconcile now
          </v-btn>
        </div>
        <v-alert type="info" dense outlined>
          Mappings use immutable directory IDs. A preview never adopts or removes a manual role
          assignment, and Apply re-reads the directory before changing managed grants.
        </v-alert>

        <v-simple-table v-if="groupMappings.length" dense data-testid="ldap-group-mappings">
          <thead>
            <tr>
              <th>Mapping</th>
              <th>Immutable group ID</th>
              <th>Role target</th>
              <th class="text-right">Actions</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="mapping in groupMappings" :key="mapping.id">
              <td>{{ mapping.id }}</td>
              <td class="ldap-stable-id">{{ mapping.group_external_id }}</td>
              <td>{{ mappingTargetLabel(mapping.target) }}</td>
              <td class="text-right text-no-wrap">
                <v-btn
                  icon
                  small
                  :aria-label="`Edit ${mapping.id}`"
                  @click="editGroupMapping(mapping)"
                >
                  <v-icon small>mdi-pencil</v-icon>
                </v-btn>
                <v-btn
                  icon
                  small
                  :aria-label="`Delete ${mapping.id}`"
                  @click="deleteGroupMapping(mapping)"
                >
                  <v-icon small>mdi-delete</v-icon>
                </v-btn>
              </td>
            </tr>
          </tbody>
        </v-simple-table>
        <v-alert v-else type="info" dense outlined>
          No LDAP group mappings are configured for this provider.
        </v-alert>

        <v-form class="mt-4" data-testid="ldap-group-mapping-editor">
          <v-row dense>
            <v-col cols="12" md="4">
              <v-text-field
                v-model="mappingForm.id"
                data-testid="ldap-group-mapping-id"
                label="Mapping ID"
                :disabled="mappingForm.expected_revision > 0"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" md="8">
              <v-text-field
                v-model="mappingForm.group_external_id"
                data-testid="ldap-group-external-id"
                label="Immutable group ID"
                placeholder="entryuuid:00000000-0000-0000-0000-000000000000"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" sm="6" md="3">
              <v-select
                v-model="mappingForm.scope"
                :items="mappingScopes"
                label="Role scope"
                outlined
                dense
                @change="mappingScopeChanged"
              />
            </v-col>
            <v-col v-if="mappingForm.scope === 'project'" cols="12" sm="6" md="4">
              <v-select
                v-model="mappingForm.project_id"
                :items="projects"
                item-text="name"
                item-value="id"
                label="Project"
                outlined
                dense
                @change="loadProjectRoles"
              />
            </v-col>
            <v-col cols="12" sm="6" :md="mappingForm.scope === 'project' ? 4 : 6">
              <v-select
                v-model="mappingForm.role_id"
                data-testid="ldap-group-role"
                :items="mappingRoleOptions"
                item-text="name"
                item-value="id"
                label="Role"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" sm="6" md="2">
              <v-switch v-model="mappingForm.enabled" label="Enabled" inset />
            </v-col>
          </v-row>
          <v-btn
            data-testid="ldap-group-mapping-save"
            color="primary"
            :loading="mappingSaving"
            :disabled="!canSaveGroupMapping"
            @click="saveGroupMapping"
          >
            Save mapping
          </v-btn>
          <v-btn v-if="mappingForm.expected_revision" text @click="resetMappingForm">
            Cancel edit
          </v-btn>
        </v-form>

        <template v-if="groupPreview">
          <v-divider class="my-6" />
          <h3 class="subtitle-1 mb-3">Latest dry-run</h3>
          <div class="ldap-preview-counts mb-3" data-testid="ldap-group-preview-counts">
            <v-chip small>Add {{ groupPreview.additions.length }}</v-chip>
            <v-chip small>Remove {{ groupPreview.removals.length }}</v-chip>
            <v-chip small :color="groupPreview.unresolved.length ? 'warning' : undefined">
              Unresolved {{ groupPreview.unresolved.length }}
            </v-chip>
            <v-chip small :color="groupPreview.collisions.length ? 'warning' : undefined">
              Collisions {{ groupPreview.collisions.length }}
            </v-chip>
            <v-chip
              small
              :color="groupPreview.protected_admin_violations.length ? 'error' : undefined"
            >
              Protected admins {{ groupPreview.protected_admin_violations.length }}
            </v-chip>
          </div>
          <v-alert
            v-if="groupPreview.unresolved.length"
            data-testid="ldap-group-unresolved"
            type="warning"
            dense
            outlined
          >
            <div class="font-weight-medium mb-1">
              Resolve these directory references before Apply:
            </div>
            <div
              v-for="item in groupPreview.unresolved"
              :key="`${item.kind}:${item.external_id}:${item.mapping_id}`"
            >
              {{ item.kind }} · {{ item.external_id }} · {{ item.reason }}
            </div>
          </v-alert>
          <v-alert v-if="groupPreview.collisions.length" type="warning" dense outlined>
            Existing manual or conflicting project memberships were preserved. Edit the mapping
            or membership, then run a new preview.
          </v-alert>
          <v-alert
            v-if="groupPreview.protected_admin_violations.length"
            type="error"
            dense
            outlined
          >
            Apply is blocked because it would remove the last effective administrator.
          </v-alert>
          <v-btn
            data-testid="ldap-group-apply"
            color="primary"
            :loading="groupApplyLoading"
            :disabled="!canApplyGroupPreview"
            @click="applyGroupPreview"
          >
            Apply preview
          </v-btn>
        </template>

        <template v-if="groupHistory.length">
          <v-divider class="my-6" />
          <h3 class="subtitle-1 mb-3">Reconciliation history</h3>
          <v-simple-table dense data-testid="ldap-group-history">
            <thead>
              <tr><th>Time</th><th>Source</th><th>Status</th><th>Changes</th><th>Issue</th></tr>
            </thead>
            <tbody>
              <tr v-for="entry in groupHistory" :key="entry.id">
                <td>{{ formatTime(entry.created) }}</td>
                <td>{{ entry.source }}</td>
                <td>{{ entry.status }}</td>
                <td>+{{ entry.addition_count }} / -{{ entry.removal_count }}</td>
                <td>{{ entry.error_code || '—' }}</td>
              </tr>
            </tbody>
          </v-simple-table>
        </template>
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
    group_search_base_dn: '',
    group_user_filter: '(objectClass=person)',
    group_filter: '(objectClass=groupOfNames)',
    group_identity_attribute: 'entryUUID',
    group_member_attribute: 'member',
    group_max_depth: 4,
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
      groupMappings: [],
      groupHistory: [],
      groupPreview: null,
      projects: [],
      globalRoles: [],
      projectRoles: [],
      mappingForm: this.emptyGroupMapping(),
      mappingSaving: false,
      groupPreviewLoading: false,
      groupApplyLoading: false,
      groupReconcileLoading: false,
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
      mappingScopes: [
        { text: 'Global role', value: 'global' },
        { text: 'Project role', value: 'project' },
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
    mappingRoleOptions() {
      return this.mappingForm.scope === 'project' ? this.projectRoles : this.globalRoles;
    },
    canSaveGroupMapping() {
      return Boolean(this.mappingForm.id
        && this.mappingForm.group_external_id
        && this.mappingForm.role_id
        && (this.mappingForm.scope !== 'project' || this.mappingForm.project_id));
    },
    canApplyGroupPreview() {
      return Boolean(this.groupPreview
        && this.groupPreview.token
        && !this.groupPreview.unresolved.length
        && !this.groupPreview.collisions.length
        && !this.groupPreview.protected_admin_violations.length);
    },
  },

  created() {
    this.load();
  },

  methods: {
    emptyProvider,

    emptyGroupMapping() {
      return {
        id: '',
        group_external_id: '',
        scope: 'global',
        project_id: null,
        role_id: '',
        enabled: true,
        expected_revision: 0,
      };
    },

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
        await this.selectProvider();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.loading = false;
      }
    },

    async selectProvider() {
      const provider = this.providers.find((entry) => entry.id === this.selectedProviderID);
      if (provider) {
        this.applyProvider(provider);
        if (this.loadGroupMappingData) await this.loadGroupMappingData();
      }
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
      this.groupMappings = [];
      this.groupHistory = [];
      this.groupPreview = null;
      this.resetMappingForm();
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
        group_max_depth: this.form.group_max_depth,
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

    async loadGroupMappingData() {
      if (!this.selectedProviderID) return;
      try {
        const providerID = encodeURIComponent(this.selectedProviderID);
        const [mappings, history, projects, roles] = await Promise.all([
          axios.get(`/api/capabilities/ldap/group-mappings?provider_id=${providerID}`),
          axios.get(`/api/capabilities/ldap/group-mappings/history?provider_id=${providerID}`),
          axios.get('/api/projects'),
          axios.get('/api/roles'),
        ]);
        this.groupMappings = mappings.data || [];
        this.groupHistory = history.data || [];
        this.projects = projects.data || [];
        this.globalRoles = roles.data || [];
      } catch (error) {
        this.error = this.errorMessage(error);
      }
    },

    async loadProjectRoles() {
      this.mappingForm.role_id = '';
      this.projectRoles = [];
      if (!this.mappingForm.project_id) return;
      try {
        const response = await axios.get(`/api/project/${this.mappingForm.project_id}/roles`);
        this.projectRoles = response.data || [];
      } catch (error) {
        this.error = this.errorMessage(error);
      }
    },

    mappingScopeChanged() {
      this.mappingForm.project_id = null;
      this.mappingForm.role_id = '';
      this.projectRoles = [];
    },

    async editGroupMapping(mapping) {
      this.mappingForm = {
        id: mapping.id,
        group_external_id: mapping.group_external_id,
        scope: mapping.target.scope,
        project_id: mapping.target.project_id || null,
        role_id: mapping.target.role_id,
        enabled: mapping.enabled,
        expected_revision: mapping.revision,
      };
      if (this.mappingForm.scope === 'project') {
        const roleID = this.mappingForm.role_id;
        await this.loadProjectRoles();
        this.mappingForm.role_id = roleID;
      }
    },

    resetMappingForm() {
      this.mappingForm = this.emptyGroupMapping();
    },

    async saveGroupMapping() {
      this.mappingSaving = true;
      this.error = '';
      try {
        const target = { scope: this.mappingForm.scope, role_id: this.mappingForm.role_id };
        if (this.mappingForm.scope === 'project') target.project_id = this.mappingForm.project_id;
        await axios.put(
          `/api/capabilities/ldap/group-mappings/${encodeURIComponent(this.mappingForm.id)}`,
          {
            provider_id: this.selectedProviderID,
            group_external_id: this.mappingForm.group_external_id,
            target,
            enabled: this.mappingForm.enabled,
            expected_revision: this.mappingForm.expected_revision,
          },
        );
        this.resetMappingForm();
        this.groupPreview = null;
        await this.loadGroupMappingData();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.mappingSaving = false;
      }
    },

    async deleteGroupMapping(mapping) {
      this.error = '';
      try {
        const providerID = encodeURIComponent(this.selectedProviderID);
        await axios.delete(
          `/api/capabilities/ldap/group-mappings/${encodeURIComponent(mapping.id)}`
            + `?provider_id=${providerID}&expected_revision=${mapping.revision}`,
        );
        this.groupPreview = null;
        await this.loadGroupMappingData();
      } catch (error) {
        this.error = this.errorMessage(error);
      }
    },

    async previewGroupMappings() {
      this.groupPreviewLoading = true;
      this.error = '';
      try {
        const response = await axios.post('/api/capabilities/ldap/group-mappings/preview', {
          provider_id: this.selectedProviderID,
        });
        this.groupPreview = this.normalizeGroupPreview(response.data);
        await this.loadGroupHistory();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.groupPreviewLoading = false;
      }
    },

    async applyGroupPreview() {
      this.groupApplyLoading = true;
      this.error = '';
      try {
        const response = await axios.post('/api/capabilities/ldap/group-mappings/apply', {
          provider_id: this.selectedProviderID,
          preview_token: this.groupPreview.token,
        });
        this.groupPreview = this.normalizeGroupPreview(response.data);
        await this.loadGroupMappingData();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.groupApplyLoading = false;
      }
    },

    async reconcileGroupMappings() {
      this.groupReconcileLoading = true;
      this.error = '';
      try {
        const response = await axios.post('/api/capabilities/ldap/group-mappings/reconcile', {
          provider_id: this.selectedProviderID,
        });
        this.groupPreview = this.normalizeGroupPreview(response.data);
        await this.loadGroupMappingData();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.groupReconcileLoading = false;
      }
    },

    async loadGroupHistory() {
      const providerID = encodeURIComponent(this.selectedProviderID);
      const response = await axios.get(
        `/api/capabilities/ldap/group-mappings/history?provider_id=${providerID}`,
      );
      this.groupHistory = response.data || [];
    },

    normalizeGroupPreview(preview) {
      return {
        ...preview,
        additions: preview.additions || [],
        removals: preview.removals || [],
        unresolved: preview.unresolved || [],
        collisions: preview.collisions || [],
        protected_admin_violations: preview.protected_admin_violations || [],
      };
    },

    mappingTargetLabel(target) {
      if (target.scope === 'project') return `Project ${target.project_id} · ${target.role_id}`;
      return `Global · ${target.role_id}`;
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
      if (code === 'LDAP_GROUP_PREVIEW_STALE') {
        return 'The directory or mapping configuration changed. Run a new preview before Apply.';
      }
      if (code === 'LDAP_GROUP_MAPPING_COLLISION') {
        return 'A manual or conflicting role assignment was preserved. Resolve it and preview again.';
      }
      if (code === 'LDAP_GROUP_PROTECTED_ADMINISTRATOR') {
        return 'Reconciliation is blocked because it would remove the last effective administrator.';
      }
      if (code === 'LDAP_GROUP_UNRESOLVED') {
        return 'Resolve all directory users, groups, and role targets before Apply.';
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
.ldap-section-heading { gap: 8px; }
.ldap-stable-id {
  max-width: 380px;
  overflow-wrap: anywhere;
  font-family: monospace;
}
.ldap-preview-counts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
@media (max-width: 600px) {
  .ldap-toolbar { grid-template-columns: minmax(0, 1fr); }
  .readiness-grid { grid-template-columns: minmax(0, 1fr); }
  .ldap-section-heading .v-btn { margin-left: 0 !important; }
}
</style>
