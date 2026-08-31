<template>
  <v-dialog :value="value" max-width="920" scrollable @input="$emit('input', $event)">
    <v-card>
      <v-card-title class="d-flex align-center">
        <span>{{ $t('crossProjectTemplates') }}</span>
        <v-spacer />
        <v-btn icon :aria-label="$t('close')" @click="$emit('input', false)">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>

      <v-divider />

      <v-card-text class="pa-0">
        <v-progress-linear v-if="loading" indeterminate color="primary" />
        <v-alert v-if="error" type="error" text tile class="ma-4">{{ error }}</v-alert>

        <section v-if="ownerMode" class="pa-4">
          <div class="d-flex align-center mb-3">
            <div>
              <div class="text-subtitle-1">{{ $t('publishedTemplateVersions') }}</div>
              <div class="text-caption text--secondary">
                {{ $t('publishedTemplateVersionsHint') }}
              </div>
            </div>
            <v-spacer />
            <v-btn color="primary" outlined small :loading="saving" @click="publishVersion">
              <v-icon small left>mdi-tag-plus-outline</v-icon>
              {{ $t('publishVersion') }}
            </v-btn>
          </div>

          <div v-if="versions.length" class="d-flex flex-wrap mb-4">
            <v-chip
              v-for="version in versions"
              :key="version.id"
              small
              outlined
              class="mr-2 mb-2"
            >
              v{{ version.version_number }} · {{ shortFingerprint(version.content_fingerprint) }}
            </v-chip>
          </div>
          <v-alert v-else type="info" text dense>{{ $t('noPublishedTemplateVersions') }}</v-alert>

          <v-card outlined class="pa-4">
            <div class="text-subtitle-1 mb-3">{{ $t('createCrossProjectGrant') }}</div>
            <v-row dense>
              <v-col cols="12" sm="4">
                <v-text-field
                  v-model="createForm.consumerProjectId"
                  type="number"
                  min="1"
                  :label="$t('consumerProjectId')"
                  outlined
                  dense
                  hide-details="auto"
                />
              </v-col>
              <v-col cols="6" sm="4">
                <v-text-field
                  v-model.number="createForm.minVersion"
                  type="number"
                  min="1"
                  :label="$t('minimumVersion')"
                  outlined
                  dense
                  hide-details="auto"
                />
              </v-col>
              <v-col cols="6" sm="4">
                <v-text-field
                  v-model.number="createForm.maxVersion"
                  type="number"
                  min="1"
                  :label="$t('maximumVersion')"
                  outlined
                  dense
                  hide-details="auto"
                />
              </v-col>
            </v-row>
            <div class="d-flex flex-wrap align-center mt-2">
              <v-checkbox
                v-model="createForm.allowReference"
                :label="$t('allowWorkflowReference')"
                dense
                hide-details
                class="mr-5 mt-0"
              />
              <v-checkbox
                v-model="createForm.allowRun"
                :label="$t('allowWorkflowRun')"
                dense
                hide-details
                class="mr-5 mt-0"
              />
            </div>
            <v-text-field
              v-model="createForm.reason"
              :label="$t('reason')"
              :counter="512"
              outlined
              dense
              hide-details="auto"
              class="mt-3"
            />
            <div class="text-right mt-3">
              <v-btn
                color="primary"
                depressed
                :disabled="!createGrantValid || saving"
                :loading="saving"
                @click="createGrant"
              >{{ $t('createGrant') }}</v-btn>
            </div>
          </v-card>
        </section>

        <v-divider v-if="ownerMode" />

        <section class="pa-4">
          <div class="text-subtitle-1 mb-1">{{ $t('crossProjectGrants') }}</div>
          <div class="text-caption text--secondary mb-3">
            {{ $t('crossProjectGrantsHint') }}
          </div>

          <v-alert v-if="!visibleGrants.length && !loading" type="info" text dense>
            {{ $t('noCrossProjectGrants') }}
          </v-alert>

          <v-card
            v-for="grant in visibleGrants"
            :key="grant.id"
            outlined
            class="pa-3 mb-3"
          >
            <div class="d-flex flex-wrap align-center">
              <strong>#{{ grant.id }}</strong>
              <v-chip x-small class="ml-2" :color="grantStatusColor(grant.status)">
                {{ grant.status }}
              </v-chip>
              <span class="text-caption text--secondary ml-3">
                {{ $t('ownerProject') }} #{{ grant.owner_project_id }} →
                {{ $t('consumerProject') }} #{{ grant.consumer_project_id }} ·
                {{ $t('template') }} #{{ grant.template_id }} ·
                v{{ grant.min_version }}–v{{ grant.max_version }} ·
                {{ operationLabel(grant.operations) }}
              </span>
              <v-spacer />
              <v-btn
                v-if="canAcceptGrant(grant)"
                small
                text
                color="primary"
                :disabled="saving"
                @click="acceptGrant(grant)"
              >{{ $t('accept') }}</v-btn>
              <v-btn
                v-if="canEditGrant(grant)"
                small
                text
                :disabled="saving"
                @click="beginEdit(grant)"
              >{{ $t('edit') }}</v-btn>
              <v-btn
                v-if="canDeleteGrant(grant)"
                small
                text
                color="error"
                :disabled="saving"
                @click="toggleDelete(grant)"
              >{{ deletingGrantId === grant.id ? $t('cancel') : $t('delete') }}</v-btn>
            </div>

            <div v-if="grant.reason" class="text-body-2 mt-2">{{ grant.reason }}</div>

            <v-card v-if="editingGrantId === grant.id" flat class="mt-3 pa-3 grant-edit-surface">
              <v-row dense>
                <v-col cols="6">
                  <v-text-field
                    v-model.number="editForm.minVersion"
                    type="number"
                    min="1"
                    :label="$t('minimumVersion')"
                    dense
                    outlined
                  />
                </v-col>
                <v-col cols="6">
                  <v-text-field
                    v-model.number="editForm.maxVersion"
                    type="number"
                    min="1"
                    :label="$t('maximumVersion')"
                    dense
                    outlined
                  />
                </v-col>
              </v-row>
              <div class="d-flex flex-wrap">
                <v-checkbox
                  v-model="editForm.allowReference"
                  :label="$t('allowWorkflowReference')"
                  dense
                  hide-details
                  class="mr-5 mt-0"
                />
                <v-checkbox
                  v-model="editForm.allowRun"
                  :label="$t('allowWorkflowRun')"
                  dense
                  hide-details
                  class="mt-0"
                />
              </div>
              <v-text-field
                v-model="editForm.reason"
                :label="$t('reason')"
                :counter="512"
                dense
                outlined
                class="mt-3"
              />
              <div class="text-right">
                <v-btn text small @click="editingGrantId = null">{{ $t('cancel') }}</v-btn>
                <v-btn
                  color="primary"
                  depressed
                  small
                  :disabled="!editGrantValid || saving"
                  @click="updateGrant(grant)"
                >{{ $t('save') }}</v-btn>
              </div>
            </v-card>

            <div v-if="deletingGrantId === grant.id" class="text-right mt-3">
              <span class="text-caption text--secondary mr-3">
                {{ $t('deleteGrantConfirmation') }}
              </span>
              <v-btn color="error" depressed small :loading="saving" @click="deleteGrant(grant)">
                {{ $t('delete') }}
              </v-btn>
            </div>

            <div v-if="canRevokeGrant(grant)" class="d-flex align-center mt-3">
              <v-text-field
                v-model="revokeReasons[grant.id]"
                :label="$t('revocationReason')"
                dense
                outlined
                hide-details="auto"
                class="mr-2"
              />
              <v-btn
                color="warning"
                outlined
                small
                :disabled="saving || !(revokeReasons[grant.id] || '').trim()"
                @click="revokeGrant(grant)"
              >{{ $t('revoke') }}</v-btn>
            </div>
          </v-card>
        </section>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

const REFERENCE_OPERATION = 1;
const RUN_OPERATION = 2;

export default {
  props: {
    value: Boolean,
    projectId: {
      type: Number,
      required: true,
    },
    templateId: {
      type: [Number, String],
      default: null,
    },
  },
  data() {
    return {
      loading: false,
      saving: false,
      error: null,
      grants: [],
      versions: [],
      editingGrantId: null,
      deletingGrantId: null,
      revokeReasons: {},
      createForm: this.emptyGrantForm(),
      editForm: this.emptyGrantForm(),
    };
  },
  computed: {
    ownerMode() {
      return Number(this.templateId) > 0;
    },
    visibleGrants() {
      if (!this.ownerMode) return this.grants;
      const templateID = Number(this.templateId);
      return this.grants.filter((grant) => grant.owner_project_id === this.projectId
        && grant.template_id === templateID);
    },
    createGrantValid() {
      const form = this.createForm;
      return Number(form.consumerProjectId) > 0
        && Number(form.consumerProjectId) !== this.projectId
        && Number(form.minVersion) > 0
        && Number(form.maxVersion) >= Number(form.minVersion)
        && (form.allowReference || form.allowRun)
        && this.byteLength(form.reason.trim()) <= 512;
    },
    editGrantValid() {
      const form = this.editForm;
      return Number(form.minVersion) > 0
        && Number(form.maxVersion) >= Number(form.minVersion)
        && (form.allowReference || form.allowRun)
        && this.byteLength(form.reason.trim()) <= 512;
    },
  },
  watch: {
    value(open) {
      if (open) this.load();
    },
  },
  methods: {
    emptyGrantForm() {
      return {
        consumerProjectId: '',
        minVersion: 1,
        maxVersion: 1,
        allowReference: true,
        allowRun: true,
        reason: '',
      };
    },
    grantCommand(form) {
      return {
        consumer_project_id: Number(form.consumerProjectId),
        min_version: Number(form.minVersion),
        max_version: Number(form.maxVersion),
        operations: (form.allowReference ? REFERENCE_OPERATION : 0)
          | (form.allowRun ? RUN_OPERATION : 0),
        reason: form.reason.trim(),
      };
    },
    updateCommand(form, revision) {
      const command = this.grantCommand(form);
      delete command.consumer_project_id;
      return { ...command, expected_revision: revision };
    },
    async load() {
      this.loading = true;
      this.error = null;
      try {
        const requests = [axios.get(
          `/api/project/${this.projectId}/cross-project-template-grants?count=100`,
        )];
        if (this.ownerMode) {
          requests.push(axios.get(
            `/api/project/${this.projectId}/templates/${this.templateId}/versions?count=100`,
          ));
        }
        const [grants, versions] = await Promise.all(requests);
        this.grants = grants.data || [];
        this.revokeReasons = this.grants.reduce((result, grant) => ({
          ...result,
          [grant.id]: this.revokeReasons[grant.id] || '',
        }), {});
        this.versions = versions?.data || [];
        if (this.versions.length) {
          const latest = Math.max(...this.versions.map((version) => version.version_number));
          this.createForm.minVersion = latest;
          this.createForm.maxVersion = latest;
        }
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.loading = false;
      }
    },
    async mutate(action, successKey) {
      this.saving = true;
      this.error = null;
      try {
        await action();
        await this.load();
        this.$emit('changed');
        EventBus.$emit('i-snackbar', { color: 'success', text: this.$t(successKey) });
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.saving = false;
      }
    },
    publishVersion() {
      return this.mutate(
        () => axios.post(`/api/project/${this.projectId}/templates/${this.templateId}/versions`),
        'templateVersionPublished',
      );
    },
    async createGrant() {
      if (!this.createGrantValid) return;
      await this.mutate(
        () => axios.post(
          `/api/project/${this.projectId}/templates/${this.templateId}/cross-project-grants`,
          this.grantCommand(this.createForm),
        ),
        'crossProjectGrantCreated',
      );
      this.createForm = this.emptyGrantForm();
    },
    beginEdit(grant) {
      this.editingGrantId = grant.id;
      this.deletingGrantId = null;
      this.editForm = {
        consumerProjectId: grant.consumer_project_id,
        minVersion: grant.min_version,
        maxVersion: grant.max_version,
        allowReference: (grant.operations & REFERENCE_OPERATION) !== 0,
        allowRun: (grant.operations & RUN_OPERATION) !== 0,
        reason: grant.reason || '',
      };
    },
    async updateGrant(grant) {
      if (!this.editGrantValid) return;
      await this.mutate(
        () => axios.put(
          `/api/project/${this.projectId}/cross-project-template-grants/${grant.id}`,
          this.updateCommand(this.editForm, grant.revision),
        ),
        'crossProjectGrantUpdated',
      );
      this.editingGrantId = null;
    },
    acceptGrant(grant) {
      return this.mutate(
        () => axios.post(
          `/api/project/${this.projectId}/cross-project-template-grants/${grant.id}/accept`,
          { expected_revision: grant.revision },
        ),
        'crossProjectGrantAccepted',
      );
    },
    revokeGrant(grant) {
      const reason = (this.revokeReasons[grant.id] || '').trim();
      if (!reason) return null;
      return this.mutate(
        () => axios.post(
          `/api/project/${this.projectId}/cross-project-template-grants/${grant.id}/revoke`,
          { expected_revision: grant.revision, reason },
        ),
        'crossProjectGrantRevoked',
      );
    },
    toggleDelete(grant) {
      this.editingGrantId = null;
      this.deletingGrantId = this.deletingGrantId === grant.id ? null : grant.id;
    },
    async deleteGrant(grant) {
      await this.mutate(
        () => axios.delete(
          `/api/project/${this.projectId}/cross-project-template-grants/${grant.id}`,
          { params: { expected_revision: grant.revision } },
        ),
        'crossProjectGrantDeleted',
      );
      this.deletingGrantId = null;
    },
    canAcceptGrant(grant) {
      return grant.status === 'pending' && grant.consumer_project_id === this.projectId;
    },
    canEditGrant(grant) {
      return grant.status === 'pending' && grant.owner_project_id === this.projectId;
    },
    canRevokeGrant(grant) {
      return (grant.status === 'pending' || grant.status === 'active')
        && (grant.owner_project_id === this.projectId
          || grant.consumer_project_id === this.projectId);
    },
    canDeleteGrant(grant) {
      return grant.status !== 'active' && grant.owner_project_id === this.projectId;
    },
    operationLabel(operations) {
      const labels = [];
      if ((operations & REFERENCE_OPERATION) !== 0) labels.push(this.$t('reference'));
      if ((operations & RUN_OPERATION) !== 0) labels.push(this.$t('run'));
      return labels.join(' + ');
    },
    grantStatusColor(status) {
      if (status === 'active') return 'success';
      if (status === 'revoked') return 'grey';
      return 'warning';
    },
    shortFingerprint(value) {
      return value ? value.slice(7, 19) : '—';
    },
    byteLength(value) {
      return new TextEncoder().encode(value || '').length;
    },
  },
};
</script>

<style scoped>
.grant-edit-surface {
  background: rgba(133, 133, 133, 0.08);
}
</style>
