<template>
  <div v-if="available">
    <v-subheader class="px-0 mt-2">OIDC group mapping</v-subheader>
    <v-card data-testid="oidc-group-mapping" style="background: var(--highlighted-card-bg-color)">
      <v-card-text>
        <v-alert v-if="error" data-testid="oidc-group-error" type="error" dense outlined>
          {{ error }}
        </v-alert>
        <v-select
          v-model="providerId"
          data-testid="oidc-group-provider"
          :items="providers"
          item-text="display_name"
          item-value="id"
          label="OIDC provider"
          outlined
          dense
          @change="loadProviderState"
        />
        <v-alert v-if="provider && !provider.claim_configuration.path" type="info" dense outlined>
          Configure a group claim path for this provider before creating mappings.
        </v-alert>
        <template v-if="provider && provider.claim_configuration.path">
          <div class="text-caption text--secondary mb-3">
            Claim path: <code>{{ provider.claim_configuration.path }}</code>
            · Missing claim: <code>{{ provider.claim_configuration.missing_claim_policy }}</code>
          </div>

          <v-simple-table
            v-if="mappings.length"
            dense
            class="oidc-responsive-table oidc-mapping-table"
            data-testid="oidc-group-mappings"
          >
            <thead><tr><th>Claim value</th><th>Role target</th><th>Status</th><th /></tr></thead>
            <tbody>
              <tr v-for="mapping in mappings" :key="mapping.id">
                <td><code>{{ mapping.claim_value }}</code></td>
                <td>{{ targetText(mapping.target) }}</td>
                <td>{{ mapping.enabled ? 'Enabled' : 'Disabled' }}</td>
                <td class="text-right text-no-wrap">
                  <v-btn
                    icon
                    small
                    :aria-label="`Edit ${mapping.id}`"
                    @click="editMapping(mapping)"
                  >
                    <v-icon small>mdi-pencil</v-icon>
                  </v-btn>
                  <v-btn
                    icon
                    small
                    :aria-label="`Delete ${mapping.id}`"
                    @click="deleteMapping(mapping)"
                  >
                    <v-icon small>mdi-delete</v-icon>
                  </v-btn>
                </td>
              </tr>
            </tbody>
          </v-simple-table>
          <div v-else class="text-body-2 text--secondary mb-3">
            No OIDC group mappings are configured for this provider.
          </div>

          <v-row dense class="mt-2">
            <v-col cols="12" sm="6">
              <v-text-field
                v-model.trim="mappingForm.id"
                data-testid="oidc-mapping-id"
                label="Mapping ID"
                :disabled="mappingForm.expected_revision > 0"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" sm="6">
              <v-text-field
                v-model="mappingForm.claim_value"
                data-testid="oidc-claim-value"
                label="Claim value"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" sm="4">
              <v-select
                v-model="mappingForm.target.scope"
                :items="roleScopes"
                label="Role scope"
                outlined
                dense
              />
            </v-col>
            <v-col v-if="mappingForm.target.scope === 'project'" cols="12" sm="4">
              <v-text-field
                v-model.number="mappingForm.target.project_id"
                label="Project ID"
                type="number"
                min="1"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" :sm="mappingForm.target.scope === 'project' ? 4 : 8">
              <v-text-field
                v-model.trim="mappingForm.target.role_id"
                label="Role ID"
                outlined
                dense
              />
            </v-col>
          </v-row>
          <div class="d-flex align-center flex-wrap mb-4">
            <v-switch
              v-model="mappingForm.enabled"
              label="Enabled"
              dense
              hide-details
              class="mt-0"
            />
            <v-spacer />
            <v-btn text small @click="resetMappingForm">Clear</v-btn>
            <v-btn
              data-testid="oidc-mapping-save"
              color="primary"
              small
              :loading="saving"
              :disabled="!canSaveMapping"
              @click="saveMapping"
            >Save mapping</v-btn>
          </div>

          <v-divider class="my-4" />
          <div class="font-weight-medium mb-2">Redacted claim preview</div>
          <v-row dense>
            <v-col cols="12" sm="4">
              <v-text-field
                v-model.number="previewUserId"
                data-testid="oidc-preview-user"
                label="User ID"
                type="number"
                min="1"
                outlined
                dense
              />
            </v-col>
            <v-col cols="12" sm="8">
              <v-textarea
                v-model="previewClaimValues"
                data-testid="oidc-preview-claim"
                label="Group claim values (one per line)"
                hint="Only group values are sent; tokens and other claims are excluded."
                rows="2"
                outlined
                dense
              />
            </v-col>
          </v-row>
          <v-btn
            data-testid="oidc-preview-run"
            color="primary"
            outlined
            small
            :loading="previewing"
            :disabled="previewUserId <= 0"
            @click="previewMappings"
          >Preview mapping</v-btn>

          <div v-if="preview" data-testid="oidc-preview-result" class="mt-3">
            <v-alert v-if="preview.preserved" type="info" dense outlined>
              The claim was not trusted as present. Existing managed access is preserved.
            </v-alert>
            <v-row dense>
              <v-col cols="6" sm="3">
                <strong>Additions</strong><div>{{ preview.additions.length }}</div>
              </v-col>
              <v-col cols="6" sm="3">
                <strong>Removals</strong><div>{{ preview.removals.length }}</div>
              </v-col>
              <v-col cols="6" sm="3">
                <strong>Unknown</strong><div>{{ preview.unknown_values.length }}</div>
              </v-col>
              <v-col cols="6" sm="3">
                <strong>Blocked</strong><div>{{ blockedCount }}</div>
              </v-col>
            </v-row>
            <div v-if="preview.unknown_values.length" class="mt-2">
              Unknown: <code>{{ preview.unknown_values.join(', ') }}</code>
            </div>
            <v-alert v-if="blockedCount" type="warning" dense outlined class="mt-2 mb-0">
              Resolve assignment collisions or protected-administrator violations before using
              this mapping.
            </v-alert>
          </div>

          <v-divider class="my-4" />
          <v-simple-table
            v-if="assignments.length"
            dense
            class="oidc-responsive-table"
            data-testid="oidc-effective-assignments"
          >
            <thead><tr><th>User</th><th>Target</th><th>Mapping</th></tr></thead>
            <tbody>
              <tr v-for="assignment in assignments" :key="assignmentKey(assignment)">
                <td>{{ assignment.user_id }}</td>
                <td>{{ targetText(assignment.target) }}</td>
                <td><code>{{ assignment.managed_by_mapping_id }}</code></td>
              </tr>
            </tbody>
          </v-simple-table>
          <div v-else class="text-body-2 text--secondary">
            No effective OIDC-managed assignments.
          </div>

          <v-simple-table
            v-if="history.length"
            dense
            class="mt-4 oidc-responsive-table"
            data-testid="oidc-group-history"
          >
            <thead><tr><th>Status</th><th>User</th><th>Changes</th><th>Time</th></tr></thead>
            <tbody>
              <tr v-for="entry in history.slice(0, 10)" :key="entry.id">
                <td>{{ entry.status }}</td>
                <td>{{ entry.user_id }}</td>
                <td>+{{ entry.addition_count }} / −{{ entry.removal_count }}</td>
                <td>{{ new Date(entry.created).toLocaleString() }}</td>
              </tr>
            </tbody>
          </v-simple-table>
        </template>
      </v-card-text>
    </v-card>
  </div>
</template>

<script>
import axios from 'axios';

export default {
  data() {
    return {
      available: false,
      providers: [],
      providerId: '',
      mappings: [],
      history: [],
      assignments: [],
      preview: null,
      previewUserId: 0,
      previewClaimValues: '',
      mappingForm: this.emptyMapping(),
      roleScopes: [
        { text: 'Global', value: 'global' },
        { text: 'Project', value: 'project' },
      ],
      saving: false,
      previewing: false,
      error: null,
    };
  },
  computed: {
    provider() {
      return this.providers.find((item) => item.id === this.providerId) || null;
    },
    canSaveMapping() {
      return Boolean(this.mappingForm.id && this.mappingForm.claim_value
        && this.mappingForm.target.role_id
        && (this.mappingForm.target.scope !== 'project' || this.mappingForm.target.project_id > 0));
    },
    blockedCount() {
      if (!this.preview) return 0;
      return this.preview.collisions.length + this.preview.protected_admin_violations.length;
    },
  },
  created() {
    this.loadProviders();
  },
  methods: {
    emptyMapping() {
      return {
        id: '',
        claim_value: '',
        enabled: true,
        expected_revision: 0,
        target: { scope: 'global', project_id: 0, role_id: '' },
      };
    },
    normalizedPreview(preview) {
      return {
        ...preview,
        additions: preview.additions || [],
        removals: preview.removals || [],
        unknown_values: preview.unknown_values || [],
        collisions: preview.collisions || [],
        protected_admin_violations: preview.protected_admin_violations || [],
      };
    },
    async loadProviders() {
      try {
        const response = await axios.get('/api/capabilities/oidc/group-mapping/providers');
        this.providers = response.data || [];
        this.available = this.providers.length > 0;
        if (this.available) {
          this.providerId = this.providers[0].id;
          await this.loadProviderState();
        }
      } catch (error) {
        if (error.response?.status !== 404) {
          this.available = true;
          this.error = this.errorMessage(error);
        }
      }
    },
    async loadProviderState() {
      this.error = null;
      this.preview = null;
      this.resetMappingForm();
      if (!this.providerId || !this.provider?.claim_configuration?.path) {
        this.mappings = [];
        this.history = [];
        this.assignments = [];
        return;
      }
      try {
        const query = { params: { provider_id: this.providerId } };
        const [mappings, history, assignments] = await Promise.all([
          axios.get('/api/capabilities/oidc/group-mappings', query),
          axios.get('/api/capabilities/oidc/group-mappings/history', query),
          axios.get('/api/capabilities/oidc/group-mappings/assignments', query),
        ]);
        this.mappings = mappings.data || [];
        this.history = history.data || [];
        this.assignments = assignments.data || [];
      } catch (error) {
        this.error = this.errorMessage(error);
      }
    },
    editMapping(mapping) {
      this.mappingForm = {
        id: mapping.id,
        claim_value: mapping.claim_value,
        enabled: mapping.enabled,
        expected_revision: mapping.revision,
        target: {
          scope: mapping.target.scope,
          project_id: mapping.target.project_id || 0,
          role_id: mapping.target.role_id,
        },
      };
    },
    resetMappingForm() {
      this.mappingForm = this.emptyMapping();
    },
    async saveMapping() {
      this.saving = true;
      this.error = null;
      try {
        await axios.put(
          `/api/capabilities/oidc/group-mappings/${encodeURIComponent(this.mappingForm.id)}`,
          {
            provider_id: this.providerId,
            claim_value: this.mappingForm.claim_value,
            target: {
              scope: this.mappingForm.target.scope,
              role_id: this.mappingForm.target.role_id,
              ...(this.mappingForm.target.scope === 'project'
                ? { project_id: Number(this.mappingForm.target.project_id) } : {}),
            },
            enabled: this.mappingForm.enabled,
            expected_revision: this.mappingForm.expected_revision,
          },
        );
        await this.loadProviderState();
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.saving = false;
      }
    },
    async deleteMapping(mapping) {
      this.error = null;
      try {
        await axios.delete(`/api/capabilities/oidc/group-mappings/${encodeURIComponent(mapping.id)}`, {
          params: { provider_id: this.providerId, expected_revision: mapping.revision },
        });
        await this.loadProviderState();
      } catch (error) {
        this.error = this.errorMessage(error);
      }
    },
    async previewMappings() {
      this.previewing = true;
      this.error = null;
      try {
        const values = this.previewClaimValues.split('\n').map((value) => value.trim()).filter(Boolean);
        const response = await axios.post('/api/capabilities/oidc/group-mappings/preview', {
          provider_id: this.providerId,
          user_id: Number(this.previewUserId),
          claim: values,
        });
        this.preview = this.normalizedPreview(response.data);
      } catch (error) {
        this.error = this.errorMessage(error);
      } finally {
        this.previewing = false;
      }
    },
    targetText(target) {
      return target.scope === 'project'
        ? `Project ${target.project_id}: ${target.role_id}`
        : `Global: ${target.role_id}`;
    },
    assignmentKey(assignment) {
      return `${assignment.user_id}:${assignment.target.scope}:${assignment.target.project_id || 0}:${assignment.target.role_id}`;
    },
    errorMessage(error) {
      return error.response?.data?.error || error.response?.data?.message
        || error.message || 'OIDC group mapping operation failed.';
    },
  },
};
</script>

<style scoped>
::v-deep .oidc-responsive-table table {
  table-layout: fixed;
  width: 100%;
}

::v-deep .oidc-responsive-table th,
::v-deep .oidc-responsive-table td {
  overflow-wrap: anywhere;
  padding-left: 4px !important;
  padding-right: 4px !important;
  white-space: normal !important;
  word-break: break-word;
}

::v-deep .oidc-responsive-table code {
  white-space: normal !important;
}

::v-deep .oidc-mapping-table th:last-child,
::v-deep .oidc-mapping-table td:last-child {
  width: 64px;
}
</style>
