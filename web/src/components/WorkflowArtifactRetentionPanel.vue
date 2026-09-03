<template>
  <section data-testid="workflow-artifact-retention">
    <h2 v-if="projectId" class="mt-8 mb-1">{{ $t('workflowArtifactRetention') }}</h2>
    <v-subheader v-else class="px-0 mt-2">
      {{ $t('workflowArtifactRetention') }}
    </v-subheader>
    <v-divider v-if="projectId" class="mb-4" />

    <v-card outlined :style="projectId ? '' : 'background: var(--highlighted-card-bg-color)'">
      <v-card-text v-if="loading" class="py-6 text-center">
        <v-progress-circular indeterminate color="primary" size="28" />
      </v-card-text>
      <v-card-text v-else>
        <v-alert v-if="error" type="error" dense outlined>
          {{ error }}
          <v-btn text small class="ml-2" @click="load">
            {{ $t('workflowArtifactRetentionRetry') }}
          </v-btn>
        </v-alert>
        <template v-if="state">
          <div class="text-body-2 text--secondary mb-4">
            {{ projectId
              ? $t('workflowArtifactRetentionProjectHint')
              : $t('workflowArtifactRetentionGlobalHint') }}
          </div>
          <v-row dense>
            <v-col cols="12" sm="4">
              <v-text-field
                v-model.number="form.retentionHours"
                type="number"
                min="1"
                :max="maxRetentionHours"
                step="0.25"
                outlined
                dense
                data-testid="artifact-retention-hours"
                :label="$t('workflowArtifactRetentionHours')"
                :error-messages="retentionHoursError"
              />
            </v-col>
            <v-col cols="12" sm="4">
              <v-text-field
                v-model.number="form.maxArtifactMiB"
                type="number"
                min="1"
                :max="maxArtifactMiB"
                step="1"
                outlined
                dense
                data-testid="artifact-retention-file-mib"
                :label="$t('workflowArtifactMaxFileMiB')"
                :error-messages="maxArtifactError"
              />
            </v-col>
            <v-col cols="12" sm="4">
              <v-text-field
                v-model.number="form.maxRunMiB"
                type="number"
                min="1"
                :max="maxRunMiB"
                step="1"
                outlined
                dense
                data-testid="artifact-retention-run-mib"
                :label="$t('workflowArtifactMaxRunMiB')"
                :error-messages="maxRunError"
              />
            </v-col>
          </v-row>
          <div class="d-flex align-center flex-wrap">
            <span class="text-caption text--secondary mr-3">
              {{ $t('workflowArtifactRetentionRevision', { revision: expectedRevision }) }}
            </span>
            <v-spacer />
            <v-btn
              color="primary"
              small
              :loading="saving"
              :disabled="!canSave"
              data-testid="artifact-retention-save"
              @click="save"
            >{{ $t('save') }}</v-btn>
          </div>
        </template>
      </v-card-text>
    </v-card>
  </section>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

const MIB = 1024 * 1024;
const DEFAULT_RETENTION_HOURS = 30 * 24;
const MAX_RETENTION_HOURS = 10 * 365 * 24;
const MAX_ARTIFACT_MIB = 64;
const MAX_RUN_MIB = 256;

export default {
  props: {
    projectId: {
      type: Number,
      default: null,
    },
  },
  data() {
    return {
      state: null,
      form: { retentionHours: null, maxArtifactMiB: null, maxRunMiB: null },
      loading: false,
      saving: false,
      error: '',
    };
  },
  computed: {
    baseUrl() {
      return this.projectId
        ? `/api/project/${this.projectId}/workflow-artifact-retention`
        : '/api/workflow-artifact-retention';
    },
    expectedRevision() {
      const policy = this.projectId ? this.state?.project_policy : this.state?.global_policy;
      return policy?.revision || 0;
    },
    globalLimits() {
      return this.state?.global_policy || {
        retention_seconds: DEFAULT_RETENTION_HOURS * 3600,
        max_artifact_bytes: MAX_ARTIFACT_MIB * MIB,
        max_run_bytes: MAX_RUN_MIB * MIB,
      };
    },
    maxRetentionHours() {
      return this.projectId
        ? this.globalLimits.retention_seconds / 3600
        : MAX_RETENTION_HOURS;
    },
    maxArtifactMiB() {
      return this.projectId
        ? Math.floor(this.globalLimits.max_artifact_bytes / MIB)
        : MAX_ARTIFACT_MIB;
    },
    maxRunMiB() {
      return this.projectId
        ? Math.floor(this.globalLimits.max_run_bytes / MIB)
        : MAX_RUN_MIB;
    },
    retentionHoursError() {
      return this.validRetentionHours(this.form.retentionHours)
        ? '' : this.$t('workflowArtifactRetentionHoursInvalid', { max: this.maxRetentionHours });
    },
    maxArtifactError() {
      return this.validInteger(this.form.maxArtifactMiB, 1, this.maxArtifactMiB)
        ? '' : this.$t('workflowArtifactMaxFileInvalid', { max: this.maxArtifactMiB });
    },
    maxRunError() {
      if (!this.validInteger(this.form.maxRunMiB, 1, this.maxRunMiB)
        || this.form.maxRunMiB < this.form.maxArtifactMiB) {
        return this.$t('workflowArtifactMaxRunInvalid', {
          min: this.form.maxArtifactMiB || 1,
          max: this.maxRunMiB,
        });
      }
      return '';
    },
    canSave() {
      return Boolean(this.state && !this.saving && !this.retentionHoursError
        && !this.maxArtifactError && !this.maxRunError);
    },
  },
  created() {
    this.load();
  },
  methods: {
    validRetentionHours(value) {
      const seconds = Number(value) * 3600;
      return Number.isFinite(seconds) && Math.abs(seconds - Math.round(seconds)) < 0.000001
        && seconds >= 3600 && seconds <= this.maxRetentionHours * 3600;
    },
    validInteger(value, min, max) {
      return Number.isInteger(value) && value >= min && value <= max;
    },
    applyState(state) {
      this.state = state;
      const source = (this.projectId ? state.project_policy : state.global_policy)
        || state.effective;
      this.form = {
        retentionHours: source.retention_seconds / 3600,
        maxArtifactMiB: source.max_artifact_bytes / MIB,
        maxRunMiB: source.max_run_bytes / MIB,
      };
    },
    updatePayload() {
      return {
        expected_revision: this.expectedRevision,
        retention_seconds: Math.round(this.form.retentionHours * 3600),
        max_artifact_bytes: this.form.maxArtifactMiB * MIB,
        max_run_bytes: this.form.maxRunMiB * MIB,
      };
    },
    async load() {
      this.loading = true;
      this.error = '';
      try {
        this.applyState((await axios.get(this.baseUrl)).data);
      } catch (error) {
        this.error = getErrorMessage(error);
      } finally {
        this.loading = false;
      }
    },
    async save() {
      if (!this.canSave) return;
      this.saving = true;
      this.error = '';
      try {
        this.applyState((await axios.put(this.baseUrl, this.updatePayload())).data);
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: this.$t('workflowArtifactRetentionSaved'),
        });
      } catch (error) {
        if (error.response?.status === 409) {
          await this.load();
          this.error = this.$t('workflowArtifactRetentionConflict');
        } else {
          this.error = getErrorMessage(error);
        }
      } finally {
        this.saving = false;
      }
    },
  },
};
</script>
