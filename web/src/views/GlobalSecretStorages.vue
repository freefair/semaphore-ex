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
      :items="items"
      :loading="loading"
      hide-default-footer
      :items-per-page="100"
      :no-data-text="$t('globalSecretStorageNone')"
      class="mt-4"
      data-testid="global-storage-table"
    >
      <template v-slot:item.name="{ item }">
        <strong>{{ item.name }}</strong>
        <div class="text-body-small">{{ item.params.url }}</div>
      </template>
      <template v-slot:item.provider_id="{ item }">
        <code>storage-{{ item.id }}</code>
      </template>
      <template v-slot:item.routes="{ item }">
        {{ (item.params.pam_routes || []).length }}
      </template>
      <template v-slot:item.project_ids="{ item }">
        <span v-if="(item.project_ids || []).length === 0">—</span>
        <span v-else>{{ item.project_ids.map(projectName).join(', ') }}</span>
      </template>
      <template v-slot:item.actions="{ item }">
        <v-btn
          icon
          variant="text"
          :aria-label="$t('edit')"
          data-testid="global-storage-edit"
          @click="openEditor(item)"
        >
          <v-icon>mdi-pencil</v-icon>
        </v-btn>
        <v-btn
          icon
          variant="text"
          :aria-label="$t('delete')"
          data-testid="global-storage-delete"
          @click="deleteTarget = item"
        >
          <v-icon>mdi-delete</v-icon>
        </v-btn>
      </template>
    </v-data-table>

    <v-dialog v-model="editorOpen" max-width="720" persistent>
      <v-card data-testid="global-storage-dialog">
        <v-card-title>
          {{ form.id ? $t('globalSecretStorageEdit') : $t('globalSecretStorageAdd') }}
        </v-card-title>
        <v-card-text>
          <v-form ref="form">
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
              label="Infisical URL"
              placeholder="https://app.infisical.com"
              :rules="[urlRule]"
              :disabled="saving"
              data-testid="global-storage-url"
              variant="outlined"
              density="compact"
            />
            <InfisicalPAMStorageFields
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
            <div class="d-flex align-center mt-4 mb-2">
              <div class="text-body-large">{{ $t('globalSecretStorageScopes') }}</div>
              <v-spacer />
              <v-btn
                variant="text"
                color="primary"
                :disabled="saving"
                data-testid="global-storage-add-scope"
                @click="addScope"
              >
                <v-icon start>mdi-plus</v-icon>
                {{ $t('globalSecretStorageAddScope') }}
              </v-btn>
            </div>
            <v-alert
              v-if="form.params.secret_scopes.length === 0"
              type="info"
              variant="tonal"
              density="compact"
              class="mb-2"
            >
              {{ $t('globalSecretStorageNoScopes') }}
            </v-alert>
            <div
              v-for="(scope, index) in form.params.secret_scopes"
              :key="index"
              class="d-flex align-start"
              data-testid="global-storage-scope"
            >
              <v-text-field
                v-model="scope.project_id"
                :label="$t('globalSecretStorageScopeProject')"
                :rules="[scopeProjectRule]"
                :disabled="saving"
                class="mr-3"
                variant="outlined"
                density="compact"
              />
              <v-text-field
                v-model="scope.environment"
                :label="$t('globalSecretStorageScopeEnvironment')"
                placeholder="prod"
                :rules="[scopeEnvironmentRule]"
                :disabled="saving"
                class="mr-3"
                variant="outlined"
                density="compact"
              />
              <v-text-field
                v-model="scope.path"
                :label="$t('globalSecretStorageScopeFolder')"
                placeholder="apps/web"
                :disabled="saving"
                variant="outlined"
                density="compact"
              />
              <v-btn
                icon
                class="ml-1 mt-1"
                :disabled="saving"
                :aria-label="$t('delete')"
                @click="form.params.secret_scopes.splice(index, 1)"
              >
                <v-icon>mdi-delete</v-icon>
              </v-btn>
            </div>

            <v-checkbox
              v-model="form.params.allow_remote_runners"
              :label="$t('globalSecretStorageRemoteRunners')"
              :hint="$t('globalSecretStorageRemoteRunnersHint')"
              persistent-hint
              :disabled="saving"
              data-testid="global-storage-remote-runners"
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
              :hint="$t('globalSecretStorageProjectsHint')"
              persistent-hint
              :disabled="saving"
              data-testid="global-storage-projects"
              variant="outlined"
              density="compact"
              class="mt-2"
            />
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
            variant="text"
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

function emptyForm() {
  return {
    id: null,
    revision: 0,
    name: '',
    type: 'infisical',
    params: {
      url: '',
      client_id: '',
      organization_slug: '',
      session_duration: '',
      ca_certificate: '',
      pam_routes: [],
      secret_scopes: [],
      allow_remote_runners: false,
    },
    secret: '',
    source_storage_type: undefined,
    project_ids: [],
  };
}

export default {
  components: { InfisicalPAMStorageFields },

  data() {
    return {
      items: [],
      projects: [],
      loading: false,
      loadError: null,
      editorOpen: false,
      form: emptyForm(),
      secretSource: 'database',
      saving: false,
      saveError: null,
      deleteTarget: null,
      deleting: false,
      deleteError: null,
      headers: [
        { title: this.$t('name'), key: 'name' },
        { title: this.$t('globalSecretStorageProviderId'), key: 'provider_id', sortable: false },
        { title: this.$t('infisicalPamRoutes'), key: 'routes', sortable: false },
        { title: this.$t('globalSecretStorageProjects'), key: 'project_ids', sortable: false },
        {
          title: '', key: 'actions', sortable: false, align: 'end',
        },
      ],
    };
  },

  async created() {
    await this.load();
  },

  methods: {
    async load() {
      this.loading = true;
      this.loadError = null;
      try {
        const [storages, projects] = await Promise.all([
          axios.get('/api/global-secret-storages'),
          axios.get('/api/projects'),
        ]);
        this.items = storages.data || [];
        this.projects = projects.data || [];
      } catch (error) {
        this.loadError = getErrorMessage(error);
      } finally {
        this.loading = false;
      }
    },

    addScope() {
      this.form.params.secret_scopes.push({ project_id: '', environment: '', path: '' });
    },

    scopeProjectRule(value) {
      return !!(value || '').trim() || this.$t('globalSecretStorageScopeProjectRequired');
    },

    scopeEnvironmentRule(value) {
      return /^[a-z0-9][a-z0-9_-]{0,63}$/.test(value || '')
        || this.$t('globalSecretStorageScopeEnvironmentInvalid');
    },

    urlRule(value) {
      return /^https:\/\/\S+$/.test(value || '') || this.$t('globalSecretStorageUrlInvalid');
    },

    projectName(id) {
      return (this.projects.find((project) => project.id === id) || {}).name || `#${id}`;
    },

    openEditor(item) {
      const form = emptyForm();
      if (item) {
        Object.assign(form, JSON.parse(JSON.stringify(item)));
        form.params = { ...emptyForm().params, ...form.params };
        form.params.secret_scopes = form.params.secret_scopes || [];
        form.secret = '';
      }
      this.form = form;
      this.secretSource = 'database';
      this.saveError = null;
      this.editorOpen = true;
    },

    async validateForm() {
      const { valid } = await this.$refs.form.validate();
      return valid;
    },

    async save() {
      if (!(await this.validateForm())) return;
      this.saving = true;
      this.saveError = null;
      const body = {
        ...this.form,
        source_storage_type: this.secretSource === 'database' ? undefined : this.secretSource,
      };
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
