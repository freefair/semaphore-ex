<template>
  <div>
    <v-toolbar flat>
      <v-toolbar-title>{{ $t('globalSecretStorages') }}</v-toolbar-title>
      <v-spacer />
      <v-btn
        color="primary"
        data-testid="global-storage-create"
        @click="openEditor(null)"
      >
        <v-icon class="d-sm-none">mdi-plus</v-icon>
        <span class="d-none d-sm-inline">{{ $t('globalSecretStorageAdd') }}</span>
      </v-btn>
    </v-toolbar>
    <v-divider />

    <v-alert
      v-if="loadError"
      type="error"
      variant="tonal"
      class="PageAlert"
      data-testid="global-storage-load-error"
    >
      {{ loadError }}
    </v-alert>

    <v-data-table
      :headers="headers"
      :items="rows"
      :loading="loading"
      hide-default-footer
      :items-per-page="100"
      :no-data-text="$t('globalSecretStorageNone')"
      class="mt-4"
      data-testid="global-storage-table"
    >
      <template v-slot:item.name="{ item }">
        <strong>{{ item.name }}</strong>
        <div class="text-body-small text-medium-emphasis">{{ item.address }}</div>
        <v-alert
          v-if="rowTests[item.key]"
          :type="rowTests[item.key].ok ? 'success' : 'error'"
          variant="tonal"
          density="compact"
          class="mt-1 mb-1"
          data-testid="global-storage-row-test"
        >
          {{ rowTests[item.key].text }}
        </v-alert>
      </template>
      <template v-slot:item.type="{ item }">
        {{ typeLabel(item.type) }}
        <v-chip
          v-if="item.source === 'configuration'"
          size="small"
          class="ml-1"
          data-testid="global-storage-config-chip"
        >
          {{ $t('globalSecretStorageFromConfiguration') }}
        </v-chip>
      </template>
      <template v-slot:item.scopes="{ item }">
        <span v-if="item.source === 'configuration'" class="text-medium-emphasis">—</span>
        <span v-else-if="item.scopeCount === 0" class="text-medium-emphasis">
          {{ $t('globalSecretStorageScopesNone') }}
        </span>
        <span v-else>{{ $t('globalSecretStorageScopesCount', { count: item.scopeCount }) }}</span>
      </template>
      <template v-slot:item.routes="{ item }">
        <span v-if="item.type !== 'infisical'" class="text-medium-emphasis">—</span>
        <span v-else>
          {{ item.routeCount }}
          <span v-if="item.projectIds.length" class="text-medium-emphasis">
            · {{ item.projectIds.map(projectName).join(', ') }}
          </span>
        </span>
      </template>
      <template v-slot:item.actions="{ item }">
        <div v-if="item.source === 'database'" class="d-flex justify-end">
          <v-btn
            icon
            variant="text"
            :loading="rowTesting === item.key"
            :aria-label="$t('globalSecretStorageTest')"
            data-testid="global-storage-row-test-button"
            @click="testRow(item)"
          >
            <v-icon>mdi-connection</v-icon>
          </v-btn>
          <v-btn
            icon
            variant="text"
            :aria-label="$t('edit')"
            data-testid="global-storage-edit"
            @click="openEditor(item.storage)"
          >
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
          <v-btn
            icon
            variant="text"
            class="ml-2"
            :aria-label="$t('delete')"
            data-testid="global-storage-delete"
            @click="deleteTarget = item.storage"
          >
            <v-icon>mdi-delete</v-icon>
          </v-btn>
        </div>
      </template>
    </v-data-table>

    <v-dialog v-model="editorOpen" max-width="860" persistent scrollable>
      <v-card data-testid="global-storage-dialog">
        <v-card-title>
          {{ form.id ? $t('globalSecretStorageEdit') : $t('globalSecretStorageAdd') }}
        </v-card-title>
        <v-card-text>
          <v-form ref="form">
            <div class="text-title-small mb-2">
              {{ $t('globalSecretStorageSectionConnection') }}
            </div>
            <v-btn-toggle
              :model-value="form.type"
              mandatory
              color="primary"
              variant="outlined"
              divided
              density="comfortable"
              class="mb-4"
              :disabled="saving || !!form.id"
              data-testid="global-storage-type"
              @update:model-value="selectType"
            >
              <v-btn value="infisical" data-testid="global-storage-type-infisical">Infisical</v-btn>
              <v-btn value="vault" data-testid="global-storage-type-vault">Vault</v-btn>
              <v-btn value="openbao" data-testid="global-storage-type-openbao">OpenBao</v-btn>
            </v-btn-toggle>
            <v-text-field
              v-model="form.name"
              :label="$t('name')"
              :rules="[(v) => !!(v || '').trim() || $t('name_required')]"
              :disabled="saving"
              data-testid="global-storage-name"
              variant="outlined"
              density="compact"
            />
            <v-text-field
              v-model="form.params.url"
              :label="addressLabel"
              :placeholder="addressPlaceholder"
              :rules="[urlRule]"
              :disabled="saving"
              data-testid="global-storage-url"
              variant="outlined"
              density="compact"
            />
            <InfisicalPAMStorageFields
              v-if="form.type === 'infisical'"
              section="connection"
              v-model:client-id="form.params.client_id"
              v-model:organization-slug="form.params.organization_slug"
              v-model:session-duration="form.params.session_duration"
              v-model:ca-certificate="form.params.ca_certificate"
              v-model:routes="form.params.pam_routes"
              v-model:secret="form.secret"
              v-model:secret-storage="secretSource"
              :is-new="!form.id"
              :form-saving="saving"
            />
            <template v-else>
              <v-text-field
                v-model="form.params.namespace"
                :label="$t('globalSecretStorageNamespace')"
                :disabled="saving"
                data-testid="global-storage-namespace"
                variant="outlined"
                density="compact"
              />
              <RuntimeProviderAuthFields
                v-model:ca-certificate="form.params.ca_certificate"
                v-model:timeout="form.params.timeout"
                v-model:auth-method="form.params.auth_method"
                v-model:auth-mount="form.params.auth_mount"
                v-model:role-id="form.params.role_id"
                v-model:role="form.params.role"
                v-model:secret-storage="secretSource"
                :runtime-credential-label="credentialLabel"
                :runtime-auth-methods="authMethods"
                :form-saving="saving"
              />
              <v-text-field
                v-model="form.secret"
                :class="secretSource === 'database' ? 'masked-secret-input' : ''"
                :label="credentialInputLabel"
                :hint="form.id ? $t('globalSecretStorageKeepCredential') : ''"
                persistent-hint
                :rules="[(v) => !!v || !!form.id || $t('globalSecretStorageCredentialRequired')]"
                :disabled="saving"
                data-testid="global-storage-credential"
                variant="outlined"
                density="compact"
              />
            </template>

            <div class="d-flex align-center flex-wrap mt-2 mb-6">
              <v-btn
                variant="outlined"
                color="primary"
                prepend-icon="mdi-connection"
                :loading="testing"
                :disabled="saving"
                data-testid="global-storage-test"
                @click="testDraft"
              >
                {{ $t('globalSecretStorageTest') }}
              </v-btn>
              <div
                v-if="testResult"
                class="ml-3 text-body-medium"
                :class="testResult.ok ? 'text-success' : 'text-error'"
                role="status"
                aria-live="polite"
                data-testid="global-storage-test-result"
              >
                <v-icon size="small" class="mr-1">
                  {{ testResult.ok ? 'mdi-check-circle' : 'mdi-alert-circle' }}
                </v-icon>
                {{ testResult.text }}
              </div>
            </div>

            <v-divider class="mb-4" />
            <div class="d-flex align-center mb-1">
              <div class="text-title-small">{{ $t('globalSecretStorageScopes') }}</div>
              <v-spacer />
              <v-btn
                v-if="!scopeEditor"
                variant="text"
                color="primary"
                prepend-icon="mdi-plus"
                :disabled="saving"
                data-testid="global-storage-add-scope"
                @click="openScopeEditor"
              >
                {{ $t('globalSecretStorageAddScope') }}
              </v-btn>
            </div>
            <div class="text-body-medium text-medium-emphasis mb-3">
              {{ $t('globalSecretStorageScopesHint') }}
            </div>
            <v-alert
              v-if="form.params.secret_scopes.length === 0 && !scopeEditor"
              type="info"
              variant="tonal"
              density="compact"
              class="mb-3"
              data-testid="global-storage-no-scopes"
            >
              {{ noScopesText }}
            </v-alert>
            <v-list v-if="form.params.secret_scopes.length" density="compact" class="py-0 mb-3">
              <v-list-item
                v-for="(scope, index) in form.params.secret_scopes"
                :key="scopeKey(scope)"
                :title="scopeLabel(scope)"
                :subtitle="scopeIdentifiers(scope)"
                prepend-icon="mdi-folder-key-outline"
                data-testid="global-storage-scope"
              >
                <template v-slot:append>
                  <v-btn
                    icon
                    variant="text"
                    size="small"
                    :disabled="saving"
                    :aria-label="$t('delete')"
                    data-testid="global-storage-remove-scope"
                    @click="form.params.secret_scopes.splice(index, 1)"
                  >
                    <v-icon>mdi-delete</v-icon>
                  </v-btn>
                </template>
              </v-list-item>
            </v-list>
            <v-card
              v-if="scopeEditor"
              variant="tonal"
              class="pa-3 mb-4"
              data-testid="global-storage-scope-editor"
            >
              <SecretLocationPicker
                v-if="!scopeManual"
                v-model="scopeDraft"
                :store-type="form.type"
                :load="scopeLoader"
                target="folder"
                @summary="scopeSummary = $event"
                @manual="scopeManual = true"
              />
              <template v-else>
                <v-text-field
                  v-model="scopeDraft.project"
                  :label="scopeRootLabel"
                  variant="outlined"
                  density="compact"
                  data-testid="global-storage-scope-manual-project"
                />
                <v-text-field
                  v-if="form.type === 'infisical'"
                  v-model="scopeDraft.environment"
                  :label="$t('globalSecretStorageScopeEnvironment')"
                  placeholder="prod"
                  variant="outlined"
                  density="compact"
                />
                <v-text-field
                  v-model="scopeDraft.path"
                  :label="$t('globalSecretStorageScopeFolder')"
                  placeholder="apps/web"
                  variant="outlined"
                  density="compact"
                />
              </template>
              <div class="d-flex justify-end">
                <v-btn variant="text" @click="closeScopeEditor">{{ $t('cancel') }}</v-btn>
                <v-btn
                  color="primary"
                  variant="flat"
                  class="ml-2"
                  :disabled="!scopeComplete"
                  data-testid="global-storage-scope-confirm"
                  @click="addScope"
                >
                  {{ $t('globalSecretStorageUseScope') }}
                </v-btn>
              </div>
            </v-card>

            <template v-if="form.type === 'infisical'">
              <v-divider class="mb-4" />
              <div class="text-title-small mb-2">{{ $t('globalSecretStorageSectionRoutes') }}</div>
              <InfisicalPAMStorageFields
                section="routes"
                v-model:routes="form.params.pam_routes"
                :is-new="!form.id"
                :form-saving="saving"
              />
              <v-autocomplete
                v-model="form.project_ids"
                :items="projects"
                item-title="name"
                item-value="id"
                multiple
                chips
                closable-chips
                :label="$t('globalSecretStorageProjects')"
                :disabled="saving"
                data-testid="global-storage-projects"
                variant="outlined"
                density="compact"
                class="mt-4"
              />
              <v-checkbox
                v-model="form.params.allow_remote_runners"
                :label="$t('globalSecretStorageRemoteRunners')"
                :hint="$t('globalSecretStorageRemoteRunnersHint')"
                persistent-hint
                :disabled="saving"
                data-testid="global-storage-remote-runners"
              />
            </template>
          </v-form>
          <v-alert
            v-if="saveError"
            type="error"
            variant="tonal"
            density="compact"
            class="mt-4"
            data-testid="global-storage-save-error"
          >
            {{ saveError }}
          </v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="saving" @click="editorOpen = false">
            {{ $t('cancel') }}
          </v-btn>
          <v-btn
            color="primary"
            variant="flat"
            :loading="saving"
            data-testid="global-storage-save"
            @click="save"
          >
            {{ $t('save') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog :model-value="deleteTarget != null" max-width="480" persistent>
      <v-card v-if="deleteTarget" data-testid="global-storage-delete-dialog">
        <v-card-title>
          {{ $t('globalSecretStorageDeleteTitle', { name: deleteTarget.name }) }}
        </v-card-title>
        <v-card-text>
          {{ $t('globalSecretStorageDeleteText') }}
          <v-alert
            v-if="deleteError"
            type="error"
            variant="tonal"
            density="compact"
            class="mt-4"
          >
            {{ deleteError }}
          </v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="deleting" @click="closeDelete">{{ $t('cancel') }}</v-btn>
          <v-btn
            color="error"
            variant="text"
            :loading="deleting"
            data-testid="global-storage-delete-confirm"
            @click="remove"
          >
            {{ $t('delete') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';
import InfisicalPAMStorageFields from '@/components/enhanced/InfisicalPAMStorageFields.vue';
import RuntimeProviderAuthFields from '@/components/enhanced/RuntimeProviderAuthFields.vue';
import SecretLocationPicker from '@/components/enhanced/SecretLocationPicker.vue';
import { browseError, draftLoader } from '@/lib/enhanced/secret-browse';

const TYPE_LABELS = { infisical: 'Infisical', vault: 'Vault', openbao: 'OpenBao' };

function emptyParams(type) {
  if (type === 'infisical') {
    return {
      url: '',
      client_id: '',
      organization_slug: '',
      session_duration: '',
      ca_certificate: '',
      pam_routes: [],
      secret_scopes: [],
      allow_remote_runners: false,
    };
  }
  return {
    url: '',
    namespace: '',
    ca_certificate: '',
    timeout: '',
    auth_method: 'token',
    auth_mount: '',
    role_id: '',
    role: '',
    secret_scopes: [],
  };
}

function emptyForm(type = 'infisical') {
  return {
    id: null,
    revision: 0,
    name: '',
    type,
    params: emptyParams(type),
    secret: '',
    project_ids: [],
  };
}

function emptyScope() {
  return {
    project: '', environment: '', mount: '', path: '',
  };
}

// The request body keeps only the settings of the connection's type, so a
// Vault connection never carries Infisical fields and the reverse.
export function storageBody(form, secretSource) {
  const params = { ...form.params };
  Object.keys(params).forEach((key) => {
    if (params[key] === '' || params[key] == null) {
      delete params[key];
    }
  });
  params.secret_scopes = form.params.secret_scopes || [];
  return {
    ...form,
    params,
    project_ids: form.type === 'infisical' ? form.project_ids : [],
    source_storage_type: secretSource === 'database' ? undefined : secretSource,
  };
}

export default {
  components: { InfisicalPAMStorageFields, RuntimeProviderAuthFields, SecretLocationPicker },

  data() {
    return {
      items: [],
      configured: [],
      projects: [],
      loading: false,
      loadError: null,
      editorOpen: false,
      form: emptyForm(),
      secretSource: 'database',
      saving: false,
      saveError: null,
      testing: false,
      testResult: null,
      rowTesting: null,
      rowTests: {},
      scopeEditor: false,
      scopeManual: false,
      scopeDraft: emptyScope(),
      scopeSummary: '',
      scopeLoader: null,
      scopeNames: {},
      deleteTarget: null,
      deleting: false,
      deleteError: null,
      authMethods: [
        { value: 'token', text: 'Token' },
        { value: 'approle', text: 'AppRole' },
        { value: 'kubernetes', text: 'Kubernetes JWT' },
      ],
      headers: [
        { title: this.$t('name'), key: 'name' },
        { title: this.$t('type'), key: 'type', sortable: false },
        { title: this.$t('globalSecretStorageScopesColumn'), key: 'scopes', sortable: false },
        { title: this.$t('globalSecretStorageRoutesColumn'), key: 'routes', sortable: false },
        {
          title: '', key: 'actions', sortable: false, align: 'end',
        },
      ],
    };
  },

  computed: {
    rows() {
      const stored = this.items.map((storage) => ({
        key: `storage-${storage.id}`,
        source: 'database',
        storage,
        name: storage.name,
        type: storage.type,
        address: (storage.params || {}).url,
        scopeCount: ((storage.params || {}).secret_scopes || []).length,
        routeCount: ((storage.params || {}).pam_routes || []).length,
        projectIds: storage.project_ids || [],
      }));
      const fromFile = this.configured.map((provider) => ({
        key: `config-${provider.provider_id}`,
        source: 'configuration',
        name: provider.name,
        type: provider.provider,
        address: '',
        scopeCount: 0,
        routeCount: 0,
        projectIds: [],
      }));
      return [...stored, ...fromFile];
    },
    addressLabel() {
      return this.$t('globalSecretStorageAddress', { type: TYPE_LABELS[this.form.type] });
    },
    addressPlaceholder() {
      return this.form.type === 'infisical' ? 'https://app.infisical.com' : 'https://vault.example.com:8200';
    },
    credentialLabel() {
      switch (this.form.params.auth_method) {
        case 'approle':
          return 'AppRole secret ID';
        case 'kubernetes':
          return 'Kubernetes JWT';
        default:
          return 'Token';
      }
    },
    credentialInputLabel() {
      if (this.secretSource === 'database') {
        return this.credentialLabel;
      }
      return this.secretSource === 'env' ? this.$t('Env var name') : this.$t('Path to the file');
    },
    noScopesText() {
      return this.form.type === 'infisical'
        ? this.$t('globalSecretStorageNoScopes')
        : this.$t('globalSecretStorageNoScopesKV');
    },
    scopeRootLabel() {
      return this.form.type === 'infisical'
        ? this.$t('globalSecretStorageScopeProject')
        : this.$t('globalSecretStorageScopeMount');
    },
    scopeComplete() {
      return this.form.type === 'infisical'
        ? !!(this.scopeDraft.project && this.scopeDraft.environment)
        : !!(this.scopeDraft.mount || this.scopeDraft.project);
    },
  },

  async created() {
    await this.load();
  },

  methods: {
    async load() {
      this.loading = true;
      this.loadError = null;
      try {
        const [storages, projects, providers] = await Promise.all([
          axios.get('/api/global-secret-storages'),
          axios.get('/api/projects'),
          axios.get('/api/global-credential-providers').catch(() => ({ data: [] })),
        ]);
        this.items = storages.data || [];
        this.projects = projects.data || [];
        this.configured = (providers.data || []).filter((provider) => provider.source === 'configuration');
      } catch (error) {
        this.loadError = getErrorMessage(error);
      } finally {
        this.loading = false;
      }
    },

    typeLabel(type) {
      return TYPE_LABELS[type] || type;
    },

    urlRule(value) {
      return /^https?:\/\/\S+$/.test(value || '') || this.$t('globalSecretStorageUrlInvalid');
    },

    projectName(id) {
      return (this.projects.find((project) => project.id === id) || {}).name || `#${id}`;
    },

    selectType(type) {
      if (!type || type === this.form.type) {
        return;
      }
      const { name } = this.form;
      this.form = { ...emptyForm(type), name };
      this.testResult = null;
      this.closeScopeEditor();
    },

    openEditor(item) {
      const form = emptyForm(item ? item.type : 'infisical');
      if (item) {
        const copy = JSON.parse(JSON.stringify(item));
        Object.assign(form, copy);
        form.params = { ...emptyParams(item.type), ...copy.params };
        form.params.secret_scopes = form.params.secret_scopes || [];
        form.secret = '';
      }
      this.form = form;
      this.secretSource = 'database';
      this.saveError = null;
      this.testResult = null;
      this.scopeNames = {};
      this.closeScopeEditor();
      this.scopeLoader = draftLoader(() => storageBody(this.form, this.secretSource));
      this.editorOpen = true;
      if (item) {
        this.resolveScopeNames();
      }
    },

    async testDraft() {
      this.testing = true;
      this.testResult = null;
      try {
        const response = await axios.post('/api/global-secret-storage-drafts/test', {
          storage: storageBody(this.form, this.secretSource),
        });
        this.testResult = { ok: true, text: this.identityText(response.data) };
        this.resolveScopeNames();
      } catch (error) {
        this.testResult = { ok: false, text: browseError(error).message };
      } finally {
        this.testing = false;
      }
    },

    async testRow(item) {
      this.rowTesting = item.key;
      try {
        const response = await axios.post(`/api/global-secret-storages/${item.storage.id}/test`);
        this.setRowTest(item, true, this.identityText(response.data));
      } catch (error) {
        this.setRowTest(item, false, browseError(error).message);
      } finally {
        this.rowTesting = null;
      }
    },

    setRowTest(item, ok, text) {
      this.rowTests = { ...this.rowTests, [item.key]: { ok, text } };
    },

    identityText(identity) {
      const parts = [this.$t('globalSecretStorageConnectedAs', { name: identity.name })];
      if (identity.description) {
        parts.push(identity.description);
      }
      return parts.join(' · ');
    },

    // resolveScopeNames looks up the names of the projects, environments and
    // mounts the scopes refer to; scopes show identifiers until it succeeds.
    async resolveScopeNames() {
      const scopes = this.form.params.secret_scopes || [];
      if (!scopes.length || !this.scopeLoader) {
        return;
      }
      try {
        const names = { ...this.scopeNames };
        const roots = await this.scopeLoader({});
        roots.forEach((node) => { names[`root:${node.value}`] = node.name; });
        if (this.form.type === 'infisical') {
          const projects = [...new Set(scopes.map((scope) => scope.project_id))];
          await Promise.all(projects.map(async (project) => {
            const environments = await this.scopeLoader({ project });
            environments.forEach((node) => { names[`env:${project}:${node.value}`] = node.name; });
          }));
        }
        this.scopeNames = names;
      } catch {
        // Names are a convenience; identifiers stay visible without them.
      }
    },

    scopeKey(scope) {
      return `${scope.project_id}|${scope.environment || ''}|${scope.path || ''}`;
    },

    scopeLabel(scope) {
      if (scope.label) {
        return scope.label;
      }
      const root = this.scopeNames[`root:${scope.project_id}`] || scope.project_id;
      const parts = [root];
      if (this.form.type === 'infisical') {
        parts.push(this.scopeNames[`env:${scope.project_id}:${scope.environment}`] || scope.environment);
      }
      parts.push(scope.path || this.$t('globalSecretStorageAllFolders'));
      return parts.join(' / ');
    },

    scopeIdentifiers(scope) {
      if (this.form.type !== 'infisical') {
        return '';
      }
      const known = this.scopeNames[`root:${scope.project_id}`];
      return known ? `${this.$t('globalSecretStorageProjectId')}: ${scope.project_id}` : '';
    },

    openScopeEditor() {
      this.scopeDraft = emptyScope();
      this.scopeSummary = '';
      this.scopeManual = false;
      this.scopeEditor = true;
    },

    closeScopeEditor() {
      this.scopeEditor = false;
      this.scopeManual = false;
      this.scopeDraft = emptyScope();
    },

    addScope() {
      const infisical = this.form.type === 'infisical';
      const scope = {
        project_id: infisical
          ? this.scopeDraft.project
          : (this.scopeDraft.mount || this.scopeDraft.project),
        environment: infisical ? this.scopeDraft.environment : '',
        path: (this.scopeDraft.path || '').replace(/^\/+|\/+$/g, ''),
      };
      const key = this.scopeKey(scope);
      if (!this.form.params.secret_scopes.some((existing) => this.scopeKey(existing) === key)) {
        this.form.params.secret_scopes.push(scope);
        if (this.scopeSummary) {
          this.labelScope(scope, this.scopeSummary);
        }
      }
      this.closeScopeEditor();
    },

    labelScope(scope, label) {
      const parts = label.split(' / ');
      const names = { ...this.scopeNames };
      names[`root:${scope.project_id}`] = parts[0];
      if (this.form.type === 'infisical' && parts[1]) {
        names[`env:${scope.project_id}:${scope.environment}`] = parts[1];
      }
      this.scopeNames = names;
    },

    async validateForm() {
      const { valid } = await this.$refs.form.validate();
      return valid;
    },

    async save() {
      if (!(await this.validateForm())) return;
      this.saving = true;
      this.saveError = null;
      const body = storageBody(this.form, this.secretSource);
      try {
        if (this.form.id) {
          await axios.put(`/api/global-secret-storages/${this.form.id}`, body);
        } else {
          await axios.post('/api/global-secret-storages', body);
        }
        this.editorOpen = false;
        await this.load();
      } catch (error) {
        this.saveError = getErrorMessage(error);
      } finally {
        this.saving = false;
      }
    },

    closeDelete() {
      this.deleteTarget = null;
      this.deleteError = null;
    },

    async remove() {
      this.deleting = true;
      this.deleteError = null;
      try {
        await axios.delete(`/api/global-secret-storages/${this.deleteTarget.id}`);
        this.closeDelete();
        await this.load();
      } catch (error) {
        this.deleteError = getErrorMessage(error);
      } finally {
        this.deleting = false;
      }
    },
  },
};
</script>
