<template>
  <div>
    <v-toolbar flat>
      <v-btn icon class="mr-4" aria-label="Back to projects" @click="returnToProjects">
        <v-icon>mdi-arrow-left</v-icon>
      </v-btn>
      <v-toolbar-title>Global credentials</v-toolbar-title>
      <v-spacer />
      <v-btn
        v-if="canCreate"
        color="primary"
        aria-label="Add credential"
        data-testid="global-credential-create"
        @click="createDialog = true"
      >
        <v-icon class="d-sm-none">mdi-plus</v-icon>
        <span class="d-none d-sm-inline">Add credential</span>
      </v-btn>
    </v-toolbar>
    <v-divider />

    <v-alert v-if="!canRead" type="warning" text class="PageAlert">
      You do not have permission to administer global credentials.
    </v-alert>

    <template v-else>
      <v-alert type="info" text class="PageAlert" data-testid="credential-value-policy">
        Credential values are write-only. This page displays metadata and an opaque version
        fingerprint, never the stored value.
      </v-alert>

      <v-data-table
        :headers="headers"
        :items="items"
        :loading="loading"
        hide-default-footer
        :items-per-page="100"
        class="mt-4"
        data-testid="global-credential-table"
      >
        <template v-slot:item.display_name="{ item }">
          <strong>{{ item.display_name }}</strong>
          <v-chip x-small class="ml-2" :color="item.enabled ? 'success' : 'grey'">
            {{ item.enabled ? 'Enabled' : 'Disabled' }}
          </v-chip>
        </template>
        <template v-slot:item.storage="{ item }">
          {{ materialKindLabel(item.material_kind) }}
        </template>
        <template v-slot:item.version="{ item }">
          v{{ item.current_version }} · <code>{{ shortFingerprint(item.fingerprint) }}</code>
        </template>
        <template v-slot:item.actions="{ item }">
          <v-btn
            icon
            small
            v-if="canMetadata"
            aria-label="Edit metadata"
            @click="openMetadata(item)"
          >
            <v-icon small>mdi-pencil</v-icon>
          </v-btn>
          <v-btn icon small v-if="canRotate" aria-label="Rotate value" @click="openRotate(item)">
            <v-icon small>mdi-refresh</v-icon>
          </v-btn>
          <v-btn icon small v-if="canGrant" aria-label="Manage grants" @click="openGrants(item)">
            <v-icon small>mdi-share-variant</v-icon>
          </v-btn>
          <v-btn icon small v-if="canAudit" aria-label="View usage" @click="openUsage(item)">
            <v-icon small>mdi-history</v-icon>
          </v-btn>
          <v-btn
            icon
            small
            v-if="canMetadata"
            :aria-label="item.enabled ? 'Disable credential' : 'Enable credential'"
            @click="openEnabledChange(item, !item.enabled)"
          >
            <v-icon small>{{ item.enabled ? 'mdi-pause-circle' : 'mdi-play-circle' }}</v-icon>
          </v-btn>
          <v-btn
            icon
            small
            v-if="canMetadata && !item.enabled"
            aria-label="Delete credential"
            @click="openDelete(item)"
          >
            <v-icon small>mdi-delete</v-icon>
          </v-btn>
        </template>
      </v-data-table>
    </template>

    <v-dialog v-model="createDialog" max-width="620" persistent>
      <v-card>
        <v-card-title>Add global credential</v-card-title>
        <v-card-text>
          <v-text-field v-model="createForm.displayName" label="Display name" maxlength="128" />
          <v-select
            v-model="createForm.materialKind"
            :items="materialKinds"
            item-text="text"
            item-value="value"
            label="Material source"
          />
          <v-text-field
            v-if="createForm.materialKind === 'local_encrypted'"
            v-model="createForm.stringValue"
            label="Credential value"
            type="password"
            maxlength="4096"
            autocomplete="new-password"
            hint="Write-only; it cannot be displayed again."
            persistent-hint
          />
          <template v-else>
            <v-select v-model="createForm.provider" :items="providers" label="Provider" />
            <v-text-field v-model="createForm.providerId" label="Provider ID" />
            <v-text-field v-model="createForm.mount" label="Mount" />
            <v-text-field v-model="createForm.path" label="Secret path" />
            <v-text-field
              v-model.number="createForm.version"
              label="Secret version"
              type="number"
              min="1"
            />
            <v-text-field v-model="createForm.field" label="Field" />
          </template>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text :disabled="createSaving" @click="closeCreate">Cancel</v-btn>
          <v-btn
            color="primary"
            :disabled="!createValid || createSaving"
            :loading="createSaving"
            @click="createCredential"
          >
            Create
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="metadataDialog" max-width="520" persistent>
      <v-card>
        <v-card-title>Edit credential metadata</v-card-title>
        <v-card-text>
          <v-text-field v-model="metadataForm.displayName" label="Display name" maxlength="128" />
          <p class="text-caption mb-0">
            Changing metadata never reads or replaces the stored value.
          </p>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="metadataDialog = false">Cancel</v-btn>
          <v-btn
            color="primary"
            :disabled="!metadataForm.displayName.trim()"
            @click="saveMetadata"
          >
            Save
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="usageDialog" max-width="980">
      <v-card>
        <v-card-title>
          Credential usage
          <span v-if="usageCredential" class="ml-2">· {{ usageCredential.display_name }}</span>
        </v-card-title>
        <v-card-text>
          <v-data-table
            :headers="usageHeaders"
            :items="usage"
            :loading="usageLoading"
            hide-default-footer
            :items-per-page="100"
            data-testid="credential-usage-table"
          >
            <template v-slot:item.identity="{ item }">
              Project #{{ item.snapshot.project_id }} · Task #{{ item.snapshot.task_id }}
            </template>
            <template v-slot:item.version="{ item }">
              {{ usageVersionLabel(item.snapshot) }}
              <template v-if="item.snapshot.credential_version">
                · <code>{{ shortFingerprint(item.snapshot.version_fingerprint) }}</code>
              </template>
            </template>
            <template v-slot:item.outcome="{ item }">
              <v-chip x-small :color="usageOutcomeColor(item.snapshot.outcome)">
                {{ item.snapshot.outcome }}
              </v-chip>
              <span class="ml-2">{{ item.snapshot.reason }}</span>
            </template>
            <template v-slot:item.occurred="{ item }">
              {{ dateLabel(item.snapshot.occurred_at) }}
            </template>
          </v-data-table>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="usageDialog = false">Close</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog :value="Boolean(enabledTarget)" max-width="520" persistent>
      <v-card>
        <v-card-title>
          {{ enabledTarget && enabledTarget.enabled ? 'Enable' : 'Disable' }} credential?
        </v-card-title>
        <v-card-text>
          <v-alert v-if="impact" dense text type="info" data-testid="credential-impact">
            {{ impactLabel(impact) }}
          </v-alert>
          Existing tasks resolve the credential only at dispatch time. Disabling it blocks new use.
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="enabledTarget = null">Cancel</v-btn>
          <v-btn color="primary" @click="confirmEnabledChange">Confirm</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="rotateDialog" max-width="620" persistent>
      <v-card>
        <v-card-title>Rotate credential</v-card-title>
        <v-card-text>
          <v-alert v-if="impact" dense text type="info" data-testid="credential-impact">
            {{ impactLabel(impact) }}
          </v-alert>
          <v-select
            v-model="rotateForm.materialKind"
            :items="materialKinds"
            item-text="text"
            item-value="value"
            label="Material source"
          />
          <v-text-field
            v-if="rotateForm.materialKind === 'local_encrypted'"
            v-model="rotateForm.stringValue"
            label="New credential value"
            type="password"
            maxlength="4096"
            autocomplete="new-password"
            hint="Write-only; submitting creates a new immutable version."
            persistent-hint
          />
          <template v-else>
            <v-select v-model="rotateForm.provider" :items="providers" label="Provider" />
            <v-text-field v-model="rotateForm.providerId" label="Provider ID" />
            <v-text-field v-model="rotateForm.mount" label="Mount" />
            <v-text-field v-model="rotateForm.path" label="Secret path" />
            <v-text-field
              v-model.number="rotateForm.version"
              label="Secret version"
              type="number"
              min="1"
            />
            <v-text-field v-model="rotateForm.field" label="Field" />
          </template>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text :disabled="rotateSaving" @click="closeRotate">Cancel</v-btn>
          <v-btn
            color="primary"
            :disabled="!rotateValid || rotateSaving"
            :loading="rotateSaving"
            @click="rotateCredential"
          >
            Rotate
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="grantsDialog" max-width="900" persistent>
      <v-card>
        <v-card-title>
          Project grants
          <span v-if="grantCredential" class="ml-2">
            · {{ grantCredential.display_name }}
          </span>
        </v-card-title>
        <v-card-text>
          <v-alert v-if="impact" dense text type="info" data-testid="credential-impact">
            {{ impactLabel(impact) }}
          </v-alert>
          <v-row>
            <v-col cols="12" md="5">
              <v-select
                v-model="grantForm.projectId"
                :items="grantProjects"
                item-text="name"
                item-value="id"
                label="Project"
              />
            </v-col>
            <v-col cols="12" md="4">
              <v-checkbox v-model="grantForm.reference" label="Reference" hide-details />
              <v-checkbox v-model="grantForm.consume" label="Consume" hide-details />
            </v-col>
            <v-col cols="12" md="3">
              <v-text-field
                v-model="grantForm.expiresAt"
                label="Expires (optional)"
                type="datetime-local"
              />
            </v-col>
          </v-row>
          <v-btn
            color="primary"
            small
            :disabled="!grantValid || grantSaving"
            :loading="grantSaving"
            @click="createGrant"
          >
            Add grant
          </v-btn>

          <v-data-table
            :headers="grantHeaders"
            :items="grants"
            hide-default-footer
            :items-per-page="100"
            class="mt-5"
          >
            <template v-slot:item.project_id="{ item }">
              {{ projectName(item.project_id) }}
            </template>
            <template v-slot:item.operations="{ item }">
              {{ operationLabel(item.operations) }}
            </template>
            <template v-slot:item.expires_at="{ item }">
              {{ dateLabel(item.expires_at) }}
            </template>
            <template v-slot:item.actions="{ item }">
              <v-btn
                text
                small
                @click="setGrantStatus(item, item.status === 'active' ? 'revoke' : 'restore')"
              >
                {{ item.status === 'active' ? 'Revoke' : 'Restore' }}
              </v-btn>
              <v-btn
                v-if="item.status === 'revoked'"
                icon
                small
                aria-label="Delete grant"
                @click="deleteGrant(item)"
              >
                <v-icon small>mdi-delete</v-icon>
              </v-btn>
            </template>
          </v-data-table>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="grantsDialog = false">Close</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog :value="Boolean(deleteTarget)" max-width="480" persistent>
      <v-card>
        <v-card-title>Delete credential?</v-card-title>
        <v-card-text>
          <v-alert v-if="impact" dense text type="info" data-testid="credential-impact">
            {{ impactLabel(impact) }}
          </v-alert>
          The credential must be disabled and all project grants must be deleted first.
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text @click="deleteTarget = null">Cancel</v-btn>
          <v-btn color="error" @click="deleteCredential">Delete</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';
import { GLOBAL_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';

function emptyMaterialForm() {
  return {
    materialKind: 'local_encrypted',
    stringValue: '',
    provider: 'vault',
    providerId: '',
    mount: '',
    path: '',
    version: 1,
    field: '',
  };
}

function emptyCreateForm() {
  return { displayName: '', ...emptyMaterialForm() };
}

export default {
  props: {
    systemInfo: Object,
    isAdmin: Boolean,
  },

  data() {
    return {
      loading: false,
      items: [],
      createDialog: false,
      createSaving: false,
      createForm: emptyCreateForm(),
      metadataDialog: false,
      metadataForm: { id: null, revision: null, displayName: '' },
      rotateDialog: false,
      rotateSaving: false,
      rotateForm: { id: null, revision: null, ...emptyMaterialForm() },
      grantsDialog: false,
      grantSaving: false,
      grantCredential: null,
      grants: [],
      grantProjects: [],
      grantForm: {
        projectId: null, reference: true, consume: false, expiresAt: '',
      },
      deleteTarget: null,
      enabledTarget: null,
      impact: null,
      usageDialog: false,
      usageLoading: false,
      usageCredential: null,
      usage: [],
      materialKinds: [
        { text: 'Encrypted local value', value: 'local_encrypted' },
        { text: 'External Vault/OpenBao reference', value: 'external_reference' },
      ],
      providers: ['vault', 'openbao'],
      headers: [
        { text: 'Name', value: 'display_name' },
        { text: 'Type', value: 'type' },
        { text: 'Storage', value: 'storage' },
        { text: 'Current version', value: 'version' },
        {
          text: '', value: 'actions', sortable: false, align: 'end',
        },
      ],
      grantHeaders: [
        { text: 'Project', value: 'project_id' },
        { text: 'Allowed operations', value: 'operations' },
        { text: 'Expires', value: 'expires_at' },
        { text: 'Status', value: 'status' },
        {
          text: '', value: 'actions', sortable: false, align: 'end',
        },
      ],
      usageHeaders: [
        { text: 'Task', value: 'identity' },
        { text: 'Target', value: 'snapshot.target' },
        { text: 'Version', value: 'version' },
        { text: 'Result', value: 'outcome' },
        { text: 'Time', value: 'occurred' },
      ],
    };
  },

  computed: {
    canMetadata() {
      return hasGlobalPermission(
        this.systemInfo,
        GLOBAL_PERMISSIONS.manageCredentialMetadata,
        this.isAdmin,
      );
    },
    canRotate() {
      return hasGlobalPermission(
        this.systemInfo,
        GLOBAL_PERMISSIONS.rotateCredentials,
        this.isAdmin,
      );
    },
    canGrant() {
      return hasGlobalPermission(
        this.systemInfo,
        GLOBAL_PERMISSIONS.grantCredentials,
        this.isAdmin,
      );
    },
    canAudit() {
      return hasGlobalPermission(
        this.systemInfo,
        GLOBAL_PERMISSIONS.readAudit,
        this.isAdmin,
      );
    },
    canRead() {
      return this.canAudit || this.canMetadata || this.canRotate || this.canGrant;
    },
    canCreate() {
      return this.canMetadata && this.canRotate;
    },
    createValid() {
      return this.createForm.displayName.trim() !== '' && this.materialValid(this.createForm);
    },
    rotateValid() {
      return this.materialValid(this.rotateForm);
    },
    grantValid() {
      return this.grantForm.projectId > 0 && (this.grantForm.reference || this.grantForm.consume);
    },
  },

  created() {
    if (this.canRead) this.load();
  },

  methods: {
    returnToProjects() {
      EventBus.$emit('i-open-last-project');
    },
    async load() {
      this.loading = true;
      try {
        this.items = (await axios.get('/api/global-credentials?count=100&offset=0')).data || [];
      } catch (error) {
        this.notifyError(error);
      } finally {
        this.loading = false;
      }
    },
    materialValid(form) {
      if (form.materialKind === 'local_encrypted') return form.stringValue.length > 0;
      return form.providerId !== '' && form.mount !== '' && form.path !== ''
        && Number(form.version) > 0 && form.field !== '';
    },
    materialPayload(form) {
      if (form.materialKind === 'local_encrypted') return { string_value: form.stringValue };
      return {
        external_reference: {
          provider: form.provider,
          provider_id: form.providerId,
          mount: form.mount,
          path: form.path,
          version: Number(form.version),
          field: form.field,
        },
      };
    },
    async createCredential() {
      if (this.createSaving) return;
      this.createSaving = true;
      try {
        await axios.post('/api/global-credentials', {
          type: 'string',
          display_name: this.createForm.displayName.trim(),
          material: this.materialPayload(this.createForm),
        });
        this.closeCreate();
        await this.load();
      } catch (error) {
        this.notifyError(error);
      } finally {
        this.createForm.stringValue = '';
        this.createSaving = false;
      }
    },
    closeCreate() {
      this.createDialog = false;
      this.createForm = emptyCreateForm();
    },
    openMetadata(item) {
      this.metadataForm = { id: item.id, revision: item.revision, displayName: item.display_name };
      this.metadataDialog = true;
    },
    async saveMetadata() {
      try {
        await axios.put(`/api/global-credentials/${this.metadataForm.id}`, {
          revision: this.metadataForm.revision,
          display_name: this.metadataForm.displayName.trim(),
        });
        this.metadataDialog = false;
        await this.load();
      } catch (error) {
        this.notifyError(error);
      }
    },
    async openRotate(item) {
      this.rotateForm = { id: item.id, revision: item.revision, ...emptyMaterialForm() };
      this.rotateDialog = true;
      await this.loadImpact(item);
    },
    async rotateCredential() {
      if (this.rotateSaving) return;
      this.rotateSaving = true;
      try {
        await axios.post(`/api/global-credentials/${this.rotateForm.id}/rotate`, {
          revision: this.rotateForm.revision,
          material: this.materialPayload(this.rotateForm),
        });
        this.closeRotate();
        await this.load();
      } catch (error) {
        this.notifyError(error);
      } finally {
        this.rotateForm.stringValue = '';
        this.rotateSaving = false;
      }
    },
    closeRotate() {
      this.rotateForm.stringValue = '';
      this.rotateDialog = false;
    },
    async setEnabled(item, enabled) {
      try {
        await axios.post(`/api/global-credentials/${item.id}/enabled`, {
          revision: item.revision,
          enabled,
        });
        await this.load();
      } catch (error) {
        this.notifyError(error);
      }
    },
    async openEnabledChange(item, enabled) {
      this.enabledTarget = { ...item, enabled };
      await this.loadImpact(item);
    },
    async confirmEnabledChange() {
      const item = this.enabledTarget;
      this.enabledTarget = null;
      if (item) await this.setEnabled(item, item.enabled);
    },
    async openDelete(item) {
      this.deleteTarget = item;
      await this.loadImpact(item);
    },
    async deleteCredential() {
      const item = this.deleteTarget;
      this.deleteTarget = null;
      if (!item) return;
      try {
        await axios.delete(`/api/global-credentials/${item.id}?expected_revision=${item.revision}`);
        await this.load();
      } catch (error) {
        this.notifyError(error);
      }
    },
    async openGrants(item) {
      this.grantCredential = item;
      this.grantForm = {
        projectId: null, reference: true, consume: false, expiresAt: '',
      };
      this.grantsDialog = true;
      await Promise.all([this.loadGrants(), this.loadImpact(item)]);
    },
    async loadImpact(item) {
      this.impact = null;
      try {
        this.impact = (await axios.get(`/api/global-credentials/${item.id}/impact`)).data;
      } catch (error) {
        this.notifyError(error);
      }
    },
    async openUsage(item) {
      this.usageCredential = item;
      this.usage = [];
      this.usageDialog = true;
      this.usageLoading = true;
      try {
        this.usage = (await axios.get(
          `/api/global-credentials/${item.id}/usage?count=100`,
        )).data || [];
      } catch (error) {
        this.notifyError(error);
      } finally {
        this.usageLoading = false;
      }
    },
    async loadGrants() {
      try {
        const [grants, projects] = await Promise.all([
          axios.get(`/api/global-credentials/${this.grantCredential.id}/grants?count=100&offset=0`),
          axios.get('/api/global-credentials/grant-projects'),
        ]);
        this.grants = grants.data || [];
        this.grantProjects = projects.data || [];
      } catch (error) {
        this.notifyError(error);
      }
    },
    async createGrant() {
      if (this.grantSaving) return;
      this.grantSaving = true;
      let operations = 0;
      if (this.grantForm.reference) operations += 1;
      if (this.grantForm.consume) operations += 2;
      try {
        await axios.post(`/api/global-credentials/${this.grantCredential.id}/grants`, {
          project_id: this.grantForm.projectId,
          operations,
          expires_at: this.grantForm.expiresAt
            ? new Date(this.grantForm.expiresAt).toISOString() : null,
        });
        this.grantForm = {
          projectId: null, reference: true, consume: false, expiresAt: '',
        };
        await Promise.all([this.loadGrants(), this.loadImpact(this.grantCredential)]);
      } catch (error) {
        this.notifyError(error);
      } finally {
        this.grantSaving = false;
      }
    },
    async setGrantStatus(grant, action) {
      try {
        await axios.post(
          `/api/global-credentials/${this.grantCredential.id}/grants/${grant.id}/${action}`,
          { revision: grant.revision },
        );
        await Promise.all([this.loadGrants(), this.loadImpact(this.grantCredential)]);
      } catch (error) {
        this.notifyError(error);
      }
    },
    async deleteGrant(grant) {
      try {
        await axios.delete(
          `/api/global-credentials/${this.grantCredential.id}/grants/${grant.id}`
            + `?expected_revision=${grant.revision}`,
        );
        await Promise.all([this.loadGrants(), this.loadImpact(this.grantCredential)]);
      } catch (error) {
        this.notifyError(error);
      }
    },
    projectName(id) {
      return (this.grantProjects.find((project) => project.id === id) || {}).name || `Project ${id}`;
    },
    operationLabel(operations) {
      if (operations === 3) return 'Reference and consume';
      if (operations === 2) return 'Consume';
      return 'Reference';
    },
    materialKindLabel(kind) {
      return kind === 'external_reference' ? 'External reference' : 'Encrypted locally';
    },
    shortFingerprint(value) {
      return value ? `${value.slice(0, 12)}…` : '—';
    },
    impactLabel(impact) {
      const last = impact.last_used_at ? ` Last used ${this.dateLabel(impact.last_used_at)}.` : '';
      return `${impact.usage_count} resolution attempts across ${impact.project_count} projects; ${impact.active_grant_count} active grants.${last}`;
    },
    usageOutcomeColor(outcome) {
      return { allowed: 'success', denied: 'warning', failure: 'error' }[outcome] || 'grey';
    },
    usageVersionLabel(snapshot) {
      return snapshot?.credential_version ? `v${snapshot.credential_version}` : 'Not resolved';
    },
    dateLabel(value) {
      return value ? new Date(value).toLocaleString() : 'Never';
    },
    notifyError(error) {
      EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(error) });
    },
  },
};
</script>
