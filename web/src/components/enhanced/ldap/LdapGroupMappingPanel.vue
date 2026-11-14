<template>
  <section data-testid="ldap-group-mappings-panel">
    <div class="ldap-mappings__toolbar">
      <h2 class="text-title-large font-weight-medium">{{ $t('ldapMappingsTitle') }}</h2>
      <v-spacer />
      <v-select
        v-if="providers.length > 1"
        v-model="providerID"
        :items="providers"
        item-title="display_name"
        item-value="id"
        :label="$t('ldapProvider')"
        variant="outlined"
        density="compact"
        hide-details
        class="ldap-mappings__provider"
        data-testid="ldap-mappings-provider"
        @update:model-value="loadMappings"
      />
      <v-btn
        color="primary"
        :disabled="!providerID"
        data-testid="ldap-group-mapping-new"
        @click="openEditor()"
      >{{ $t('ldapMappingAdd') }}</v-btn>
    </div>

    <p
      v-if="!providers.length"
      class="text-medium-emphasis"
      data-testid="ldap-mappings-no-provider">
      {{ $t('ldapMappingsNoProvider') }}
      <router-link to="/admin/authentication">{{ $t('adminAuthentication') }}</router-link>
    </p>

    <v-alert
      v-if="listError"
      type="error"
      variant="outlined"
      density="compact"
      role="alert"
      class="mb-3">
      {{ listError }}
    </v-alert>

    <template v-if="providerID">
      <p class="text-body-medium mb-3">{{ $t('ldapMappingsIntro') }}</p>

      <v-table v-if="mappings.length" density="compact" data-testid="ldap-group-mappings">
        <thead>
          <tr>
            <th>{{ $t('ldapMappingGroup') }}</th>
            <th>{{ $t('ldapMappingRole') }}</th>
            <th>{{ $t('ldapMappingState') }}</th>
            <th class="text-right">{{ $t('ldapMappingActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="mapping in mappings"
            :key="mapping.id"
            :data-testid="`ldap-mapping-${mapping.id}`">
            <td>
              <div class="font-weight-medium">{{ groupLabel(mapping) }}</div>
              <div class="text-body-small text-medium-emphasis ldap-mappings__id">
                {{ mapping.group_external_id }}
              </div>
            </td>
            <td>{{ targetLabel(mapping.target) }}</td>
            <td>
              <v-chip size="small" :color="mapping.enabled ? 'success' : undefined">
                {{ mapping.enabled ? $t('ldapMappingEnabled') : $t('ldapMappingDisabled') }}
              </v-chip>
            </td>
            <td class="text-right text-no-wrap">
              <v-btn
                variant="text"
                size="small"
                prepend-icon="mdi-pencil"
                :data-testid="`ldap-mapping-edit-${mapping.id}`"
                @click="openEditor(mapping)"
              >{{ $t('ldapMappingEdit') }}</v-btn>
              <v-btn
                variant="text"
                size="small"
                color="error"
                prepend-icon="mdi-delete"
                :data-testid="`ldap-mapping-delete-${mapping.id}`"
                @click="confirmDelete = mapping"
              >{{ $t('ldapMappingDelete') }}</v-btn>
            </td>
          </tr>
        </tbody>
      </v-table>
      <p v-else class="text-medium-emphasis" data-testid="ldap-group-mappings-empty">
        {{ $t('ldapMappingsEmpty') }}
      </p>

      <h3 class="text-title-medium font-weight-medium mt-8 mb-2">{{ $t('ldapApplyTitle') }}</h3>
      <p class="text-body-medium mb-3">{{ $t('ldapApplyIntro') }}</p>
      <div class="ldap-mappings__actions">
        <v-btn
          variant="outlined"
          :loading="previewLoading"
          :disabled="!mappings.length"
          data-testid="ldap-group-preview"
          @click="preview"
        >{{ $t('ldapPreviewChanges') }}</v-btn>
        <v-btn
          color="primary"
          :loading="applyLoading"
          :disabled="!canApply"
          data-testid="ldap-group-apply"
          @click="apply"
        >{{ $t('ldapApplyChanges') }}</v-btn>
        <span
          v-if="appliedMessage"
          class="text-success"
          role="status"
          data-testid="ldap-group-applied">
          {{ appliedMessage }}
        </span>
      </div>
      <v-alert
        v-if="applyError"
        type="error"
        variant="outlined"
        density="compact"
        role="alert"
        class="mt-3">
        {{ applyError }}
      </v-alert>

      <div v-if="previewResult" class="mt-4" data-testid="ldap-group-preview-result">
        <div class="ldap-mappings__counts" data-testid="ldap-group-preview-counts">
          <v-chip size="small" color="success">
            {{ $t('ldapPreviewAdd', { n: previewResult.additions.length }) }}
          </v-chip>
          <v-chip size="small" :color="previewResult.removals.length ? 'warning' : undefined">
            {{ $t('ldapPreviewRemove', { n: previewResult.removals.length }) }}
          </v-chip>
          <v-chip size="small">
            {{ $t('ldapPreviewSkipped', { n: previewResult.skipped.length }) }}
          </v-chip>
        </div>
        <v-table
          v-if="changes.length"
          density="compact"
          class="mt-2"
          data-testid="ldap-group-preview-changes">
          <tbody>
            <tr v-for="change in changes" :key="change.key">
              <td>
                <v-icon size="small" :color="change.kind === 'add' ? 'success' : 'warning'">
                  {{ change.kind === 'add' ? 'mdi-plus-circle' : 'mdi-minus-circle' }}
                </v-icon>
              </td>
              <td>{{ userLabel(change.user_id) }}</td>
              <td>{{ targetLabel(change.target) }}</td>
            </tr>
          </tbody>
        </v-table>
        <v-alert
          v-if="previewResult.skipped.length"
          type="info"
          variant="outlined"
          density="compact"
          class="mt-3"
          data-testid="ldap-group-skipped"
        >
          {{ $t('ldapPreviewSkippedExplanation', { n: previewResult.skipped.length }) }}
        </v-alert>
        <v-alert
          v-if="previewResult.unresolved.length"
          type="warning"
          variant="outlined"
          density="compact"
          class="mt-3"
          data-testid="ldap-group-unresolved"
        >
          <div class="font-weight-medium mb-1">{{ $t('ldapPreviewUnresolved') }}</div>
          <div
            v-for="item in previewResult.unresolved"
            :key="`${item.kind}:${item.external_id}:${item.mapping_id}`">
            {{ unresolvedText(item) }}
          </div>
        </v-alert>
        <v-alert
          v-if="previewResult.collisions.length"
          type="warning"
          variant="outlined"
          density="compact"
          class="mt-3"
        >{{ $t('ldapPreviewCollisions') }}</v-alert>
        <v-alert
          v-if="previewResult.protected_admin_violations.length"
          type="error"
          variant="outlined"
          density="compact"
          class="mt-3"
        >{{ $t('ldapPreviewProtectedAdmin') }}</v-alert>
      </div>

      <template v-if="history.length">
        <h3 class="text-title-medium font-weight-medium mt-8 mb-2">{{ $t('ldapHistoryTitle') }}</h3>
        <v-table density="compact" data-testid="ldap-group-history">
          <thead>
            <tr>
              <th>{{ $t('ldapHistoryWhen') }}</th>
              <th>{{ $t('ldapHistorySource') }}</th>
              <th>{{ $t('ldapHistoryResult') }}</th>
              <th>{{ $t('ldapHistoryChanges') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in history" :key="entry.id">
              <td>{{ formatTime(entry.created) }}</td>
              <td>{{ entry.source }}</td>
              <td>{{ entry.error_code ? errorCodeText(entry.error_code) : entry.status }}</td>
              <td>+{{ entry.addition_count }} / −{{ entry.removal_count }}</td>
            </tr>
          </tbody>
        </v-table>
      </template>
    </template>

    <v-dialog v-model="editorOpen" max-width="720" data-testid="ldap-group-mapping-dialog">
      <v-card>
        <v-card-title>
          {{ editor.expected_revision ? $t('ldapMappingEditTitle') : $t('ldapMappingAddTitle') }}
        </v-card-title>
        <v-card-text>
          <v-autocomplete
            v-model="editor.group"
            v-model:search="groupQuery"
            :items="groupResults"
            :loading="groupSearching"
            item-title="name"
            return-object
            no-filter
            :label="$t('ldapMappingGroup')"
            :hint="$t('ldapMappingGroupHint')"
            persistent-hint
            :no-data-text="groupQuery ? $t('ldapGroupSearchNone') : $t('ldapGroupSearchStart')"
            variant="outlined"
            density="compact"
            data-testid="ldap-group-picker"
            @update:search="searchGroups"
            @update:model-value="groupChosen"
          >
            <template v-slot:item="{ props, item }">
              <v-list-item v-bind="props" :subtitle="item.dn" />
            </template>
          </v-autocomplete>
          <div
            v-if="editor.group"
            class="text-body-small text-medium-emphasis mt-1 ldap-mappings__id">
            {{ editor.group.external_id }}
          </div>
          <div
            v-if="groupSearchError"
            class="text-error text-body-small mt-1">
            {{ groupSearchError }}
          </div>

          <v-radio-group
            v-model="editor.scope"
            inline
            class="mt-4"
            hide-details
            @update:model-value="scopeChanged">
            <v-radio
              value="project"
              class="mr-6"
              :label="$t('ldapMappingScopeProject')"
              data-testid="ldap-mapping-scope-project" />
            <v-radio
              value="global"
              :label="$t('ldapMappingScopeGlobal')"
              data-testid="ldap-mapping-scope-global" />
          </v-radio-group>

          <v-select
            v-if="editor.scope === 'project'"
            v-model="editor.project_id"
            :items="projects"
            item-title="name"
            item-value="id"
            :label="$t('ldapMappingProject')"
            variant="outlined"
            density="compact"
            class="mt-3"
            data-testid="ldap-mapping-project"
            @update:model-value="loadProjectRoles"
          />
          <v-select
            v-model="editor.role_id"
            :items="roleOptions"
            item-title="name"
            item-value="id"
            :label="$t('ldapMappingRole')"
            :disabled="editor.scope === 'project' && !editor.project_id"
            variant="outlined"
            density="compact"
            class="mt-3"
            data-testid="ldap-group-role"
          />
          <v-switch
            v-model="editor.enabled"
            :label="$t('ldapMappingEnabledSwitch')"
            color="primary"
            inset
            hide-details
          />
          <v-text-field
            v-model="editor.id"
            :label="$t('ldapMappingId')"
            :hint="$t('ldapMappingIdHint')"
            persistent-hint
            :disabled="Boolean(editor.expected_revision)"
            variant="outlined"
            density="compact"
            class="mt-3"
            data-testid="ldap-group-mapping-id"
          />
          <v-alert
            v-if="editorError"
            type="error"
            variant="outlined"
            density="compact"
            role="alert"
            class="mt-4"
            data-testid="ldap-mapping-error"
          >{{ editorError }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="editorOpen = false">{{ $t('cancel') }}</v-btn>
          <v-btn
            color="primary"
            :loading="editorSaving"
            :disabled="!canSave"
            data-testid="ldap-group-mapping-save"
            @click="saveMapping"
          >{{ $t('save') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog
      :model-value="Boolean(confirmDelete)"
      max-width="520"
      @update:model-value="confirmDelete = null">
      <v-card v-if="confirmDelete">
        <v-card-title>{{ $t('ldapMappingDeleteTitle') }}</v-card-title>
        <v-card-text>
          {{ $t('ldapMappingDeleteText', { group: groupLabel(confirmDelete) }) }}
        </v-card-text>
        <v-alert v-if="deleteError" type="error" variant="outlined" density="compact" class="mx-4">
          {{ deleteError }}
        </v-alert>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="confirmDelete = null">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" data-testid="ldap-mapping-delete-confirm" @click="deleteMapping">
            {{ $t('ldapMappingDelete') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </section>
</template>

<script>
import axios from 'axios';
import { ldapErrorText, LDAP_ERROR_MESSAGES, mappingIdFromName } from '@/lib/enhanced/ldap';

function emptyEditor() {
  return {
    id: '',
    group: null,
    scope: 'project',
    project_id: null,
    role_id: '',
    enabled: true,
    expected_revision: 0,
  };
}

function normalizePreview(preview) {
  return {
    ...preview,
    additions: preview.additions || [],
    removals: preview.removals || [],
    skipped: preview.skipped || [],
    unresolved: preview.unresolved || [],
    collisions: preview.collisions || [],
    protected_admin_violations: preview.protected_admin_violations || [],
  };
}

export default {
  data() {
    return {
      providers: [],
      providerID: '',
      mappings: [],
      history: [],
      users: [],
      projects: [],
      globalRoles: [],
      projectRoles: [],
      groupNames: {},
      listError: '',
      editorOpen: false,
      editor: emptyEditor(),
      editorError: '',
      editorSaving: false,
      groupQuery: '',
      groupResults: [],
      groupSearching: false,
      groupSearchError: '',
      groupSearchTimer: null,
      confirmDelete: null,
      deleteError: '',
      previewResult: null,
      previewLoading: false,
      applyLoading: false,
      applyError: '',
      appliedMessage: '',
    };
  },

  computed: {
    roleOptions() {
      return this.editor.scope === 'project' ? this.projectRoles : this.globalRoles;
    },
    canSave() {
      return Boolean(this.editor.id && this.editor.group && this.editor.role_id
        && (this.editor.scope !== 'project' || this.editor.project_id));
    },
    canApply() {
      const p = this.previewResult;
      return Boolean(p && p.token && !p.unresolved.length && !p.collisions.length
        && !p.protected_admin_violations.length);
    },
    changes() {
      if (!this.previewResult) return [];
      return [
        ...this.previewResult.additions.map((change, index) => ({ ...change, kind: 'add', key: `a${index}` })),
        ...this.previewResult.removals.map((change, index) => ({ ...change, kind: 'remove', key: `r${index}` })),
      ];
    },
  },

  async created() {
    try {
      const [providers, users, projects, roles] = await Promise.all([
        axios.get('/api/capabilities/ldap'),
        axios.get('/api/users'),
        axios.get('/api/projects'),
        axios.get('/api/roles'),
      ]);
      this.providers = providers.data || [];
      this.users = users.data || [];
      this.projects = projects.data || [];
      this.globalRoles = roles.data || [];
      this.providerID = this.providers.length ? this.providers[0].id : '';
      await this.loadMappings();
    } catch (error) {
      this.listError = ldapErrorText(error, this.$t);
    }
  },

  beforeUnmount() {
    clearTimeout(this.groupSearchTimer);
  },

  methods: {
    async loadMappings() {
      if (!this.providerID) return;
      this.listError = '';
      this.previewResult = null;
      try {
        const provider = encodeURIComponent(this.providerID);
        const [mappings, history] = await Promise.all([
          axios.get(`/api/capabilities/ldap/group-mappings?provider_id=${provider}`),
          axios.get(`/api/capabilities/ldap/group-mappings/history?provider_id=${provider}`),
        ]);
        this.mappings = mappings.data || [];
        this.history = history.data || [];
      } catch (error) {
        this.listError = ldapErrorText(error, this.$t);
      }
    },

    async openEditor(mapping) {
      this.editorError = '';
      this.groupResults = [];
      this.groupQuery = '';
      if (mapping) {
        const known = mapping.group_name || this.groupNames[mapping.group_external_id];
        this.editor = {
          id: mapping.id,
          group: {
            external_id: mapping.group_external_id,
            name: known || mapping.group_external_id,
          },
          scope: mapping.target.scope,
          project_id: mapping.target.project_id || null,
          role_id: mapping.target.role_id,
          enabled: mapping.enabled,
          expected_revision: mapping.revision,
        };
        this.groupResults = [this.editor.group];
        if (this.editor.scope === 'project') await this.loadProjectRoles(true);
      } else {
        this.editor = emptyEditor();
      }
      this.editorOpen = true;
    },

    searchGroups(query) {
      clearTimeout(this.groupSearchTimer);
      if (!query || (this.editor.group && query === this.editor.group.name)) return;
      this.groupSearchTimer = setTimeout(() => this.runGroupSearch(query), 250);
    },

    async runGroupSearch(query) {
      this.groupSearching = true;
      this.groupSearchError = '';
      try {
        const response = await axios.get('/api/capabilities/ldap/groups', {
          params: { provider_id: this.providerID, query },
        });
        this.groupResults = response.data || [];
        this.groupResults.forEach((group) => { this.groupNames[group.external_id] = group.name; });
      } catch (error) {
        this.groupSearchError = ldapErrorText(error, this.$t);
      } finally {
        this.groupSearching = false;
      }
    },

    groupChosen(group) {
      if (group && !this.editor.expected_revision) {
        this.editor.id = mappingIdFromName(group.name, this.mappings.map((mapping) => mapping.id));
      }
    },

    scopeChanged() {
      this.editor.project_id = null;
      this.editor.role_id = '';
      this.projectRoles = [];
    },

    async loadProjectRoles(keepRole) {
      if (!keepRole) this.editor.role_id = '';
      this.projectRoles = [];
      if (!this.editor.project_id) return;
      try {
        const response = await axios.get(`/api/project/${this.editor.project_id}/roles/all`);
        this.projectRoles = response.data || [];
      } catch (error) {
        this.editorError = ldapErrorText(error, this.$t);
      }
    },

    async saveMapping() {
      this.editorSaving = true;
      this.editorError = '';
      try {
        const target = { scope: this.editor.scope, role_id: this.editor.role_id };
        if (this.editor.scope === 'project') target.project_id = this.editor.project_id;
        await axios.put(`/api/capabilities/ldap/group-mappings/${encodeURIComponent(this.editor.id)}`, {
          provider_id: this.providerID,
          group_external_id: this.editor.group.external_id,
          group_name: this.editor.group.name,
          target,
          enabled: this.editor.enabled,
          expected_revision: this.editor.expected_revision,
        });
        this.groupNames[this.editor.group.external_id] = this.editor.group.name;
        this.editorOpen = false;
        await this.loadMappings();
      } catch (error) {
        this.editorError = ldapErrorText(error, this.$t);
      } finally {
        this.editorSaving = false;
      }
    },

    async deleteMapping() {
      this.deleteError = '';
      const mapping = this.confirmDelete;
      try {
        const provider = encodeURIComponent(this.providerID);
        await axios.delete(
          `/api/capabilities/ldap/group-mappings/${encodeURIComponent(mapping.id)}`
            + `?provider_id=${provider}&expected_revision=${mapping.revision}`,
        );
        this.confirmDelete = null;
        await this.loadMappings();
      } catch (error) {
        this.deleteError = ldapErrorText(error, this.$t);
      }
    },

    async preview() {
      this.previewLoading = true;
      this.applyError = '';
      this.appliedMessage = '';
      try {
        const response = await axios.post('/api/capabilities/ldap/group-mappings/preview', {
          provider_id: this.providerID,
        });
        this.previewResult = normalizePreview(response.data);
      } catch (error) {
        this.applyError = ldapErrorText(error, this.$t);
      } finally {
        this.previewLoading = false;
      }
    },

    async apply() {
      this.applyLoading = true;
      this.applyError = '';
      try {
        const response = await axios.post('/api/capabilities/ldap/group-mappings/apply', {
          provider_id: this.providerID,
          preview_token: this.previewResult.token,
        });
        const result = normalizePreview(response.data);
        this.appliedMessage = this.$t('ldapApplied', {
          added: result.additions.length, removed: result.removals.length,
        });
        this.previewResult = null;
        await this.loadMappings();
      } catch (error) {
        this.applyError = ldapErrorText(error, this.$t);
      } finally {
        this.applyLoading = false;
      }
    },

    groupLabel(mapping) {
      return mapping.group_name || this.groupNames[mapping.group_external_id] || mapping.id;
    },

    targetLabel(target) {
      if (!target) return '';
      if (target.scope === 'project') {
        const project = this.projects.find((entry) => entry.id === target.project_id);
        return this.$t('ldapTargetProject', {
          project: project ? project.name : `#${target.project_id}`,
          role: this.roleName(target.role_id),
        });
      }
      const role = this.globalRoles.find((entry) => entry.id === target.role_id);
      return this.$t('ldapTargetGlobal', { role: role ? role.name : target.role_id });
    },

    roleName(roleID) {
      const id = String(roleID || '');
      if (id.startsWith('builtin:')) {
        const slug = id.slice('builtin:'.length);
        return slug.charAt(0).toUpperCase() + slug.slice(1).replace(/_/g, ' ');
      }
      const known = this.projectRoles.find((role) => role.id === id);
      return known ? known.name : id;
    },

    userLabel(userID) {
      const user = this.users.find((entry) => entry.id === userID);
      return user ? user.name || user.username : `#${userID}`;
    },

    unresolvedText(item) {
      if (item.reason === 'directory_group_not_found') {
        return this.$t('ldapUnresolvedGroup', { group: this.groupNames[item.external_id] || item.external_id });
      }
      return `${item.kind} · ${item.external_id} · ${item.reason}`;
    },

    errorCodeText(code) {
      return LDAP_ERROR_MESSAGES[code] ? this.$t(LDAP_ERROR_MESSAGES[code]) : code;
    },

    formatTime(value) {
      const parsed = new Date(value);
      return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
    },
  },
};
</script>

<style scoped>
.ldap-mappings__toolbar,
.ldap-mappings__actions,
.ldap-mappings__counts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 12px;
}
.ldap-mappings__toolbar {
  margin-bottom: 12px;
}
.ldap-mappings__provider {
  max-width: 320px;
}
.ldap-mappings__id {
  font-family: monospace;
  overflow-wrap: anywhere;
}
</style>
