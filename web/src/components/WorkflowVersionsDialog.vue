<template>
  <v-dialog
    :value="value"
    max-width="960"
    scrollable
    @input="$emit('input', $event)"
  >
    <v-card data-testid="workflow-versions-dialog">
      <v-card-title>
        {{ $t('workflowVersionHistory') }}
        <v-spacer />
        <v-btn icon :aria-label="$t('close')" @click="$emit('input', false)">
          <v-icon>mdi-close</v-icon>
        </v-btn>
      </v-card-title>
      <v-divider />

      <v-card-text class="pa-0">
        <div v-if="loading" class="pa-8 text-center">
          <v-progress-circular indeterminate color="primary" />
        </div>
        <v-alert v-else-if="error" type="error" text class="ma-4">
          <div class="d-flex align-center">
            <span>{{ error }}</span>
            <v-spacer />
            <v-btn text color="error" @click="loadVersions">
              {{ $t('workflowVersionRetry') }}
            </v-btn>
          </div>
        </v-alert>
        <div v-else-if="versions.length === 0" class="pa-8 text-center text--secondary">
          {{ $t('workflowVersionHistoryEmpty') }}
        </div>
        <template v-else>
          <v-simple-table>
            <thead>
              <tr>
                <th>{{ $t('workflowVersion') }}</th>
                <th>{{ $t('workflowVersionMessageColumn') }}</th>
                <th>{{ $t('workflowVersionCreated') }}</th>
                <th class="text-right">{{ $t('workflowVersionActions') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="version in versions" :key="version.id">
                <td>
                  <strong>#{{ version.version_number }}</strong>
                  <v-chip
                    v-if="version.version_number === latestVersionNumber"
                    x-small
                    class="ml-2"
                  >{{ $t('workflowVersionCurrent') }}</v-chip>
                  <div v-if="version.restored_from_version_id" class="text-caption text--secondary">
                    {{ $t('workflowVersionRestored') }}
                  </div>
                </td>
                <td>{{ version.message || '—' }}</td>
                <td>{{ formatCreated(version.created) }}</td>
                <td class="text-right text-no-wrap">
                  <v-btn small text @click="selectForCompare(version)">
                    {{ $t('workflowVersionCompareAction') }}
                  </v-btn>
                  <v-btn
                    small
                    text
                    color="primary"
                    :disabled="!canRestore || version.version_number === latestVersionNumber"
                    @click="previewRestore(version)"
                  >{{ $t('workflowVersionRestoreAction') }}</v-btn>
                </td>
              </tr>
            </tbody>
          </v-simple-table>

          <v-divider />
          <div class="pa-4">
            <div class="text-subtitle-2 mb-3">{{ $t('workflowVersionCompare') }}</div>
            <div class="d-flex flex-wrap align-center WorkflowVersionsDialog__compareControls">
              <v-select
                v-model="compareFrom"
                :items="versionOptions"
                :label="$t('workflowVersionCompareFrom')"
                outlined
                dense
                hide-details
              />
              <v-icon class="mx-3">mdi-arrow-right</v-icon>
              <v-select
                v-model="compareTo"
                :items="versionOptions"
                :label="$t('workflowVersionCompareTo')"
                outlined
                dense
                hide-details
              />
              <v-btn
                class="ml-3"
                color="primary"
                outlined
                :loading="comparing"
                :disabled="!canCompare"
                @click="compareVersions"
              >{{ $t('workflowVersionCompareAction') }}</v-btn>
            </div>

            <v-alert
              v-if="comparison && comparison.changes.length === 0"
              type="info"
              text
              class="mt-4 mb-0"
            >{{ $t('workflowVersionNoChanges') }}</v-alert>
            <v-expansion-panels
              v-else-if="comparison"
              accordion
              flat
              class="mt-4"
              data-testid="workflow-version-diff"
            >
              <v-expansion-panel
                v-for="change in comparison.changes"
                :key="change.section"
              >
                <v-expansion-panel-header>{{ change.section }}</v-expansion-panel-header>
                <v-expansion-panel-content>
                  <v-row>
                    <v-col cols="12" md="6">
                      <div class="text-caption text--secondary mb-1">
                        {{ $t('workflowVersionBefore') }}
                      </div>
                      <pre>{{ pretty(change.before) }}</pre>
                    </v-col>
                    <v-col cols="12" md="6">
                      <div class="text-caption text--secondary mb-1">
                        {{ $t('workflowVersionAfter') }}
                      </div>
                      <pre>{{ pretty(change.after) }}</pre>
                    </v-col>
                  </v-row>
                </v-expansion-panel-content>
              </v-expansion-panel>
            </v-expansion-panels>
          </div>

          <template v-if="restorePreview">
            <v-divider />
            <div class="pa-4" data-testid="workflow-version-restore-preview">
              <v-alert type="warning" text>
                {{ $t('workflowVersionRestorePreview', {
                  version: restorePreview.version.version_number,
                  sections: changedSections(restorePreview.diff),
                }) }}
              </v-alert>
              <v-text-field
                v-model="restoreMessage"
                :label="$t('workflowVersionMessage')"
                :counter="512"
                :rules="[value => byteLength(value) <= 512 || $t('workflowVersionMessageTooLong')]"
                outlined
                dense
              />
              <div class="d-flex justify-end">
                <v-btn text @click="restorePreview = null">{{ $t('cancel') }}</v-btn>
                <v-btn
                  color="warning"
                  class="ml-2"
                  :loading="restoring"
                  :disabled="byteLength(restoreMessage) > 512"
                  @click="confirmRestore"
                >{{ $t('workflowVersionRestoreConfirm') }}</v-btn>
              </div>
            </div>
          </template>
        </template>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    value: Boolean,
    projectId: {
      type: Number,
      required: true,
    },
    workflowId: {
      type: Number,
      required: true,
    },
    canRestore: Boolean,
  },
  data() {
    return {
      versions: [],
      loading: false,
      error: '',
      compareFrom: null,
      compareTo: null,
      comparison: null,
      comparing: false,
      restorePreview: null,
      restoreMessage: '',
      restoring: false,
    };
  },
  computed: {
    latestVersionNumber() {
      return this.versions.reduce(
        (latest, version) => Math.max(latest, version.version_number || 0),
        0,
      );
    },
    versionOptions() {
      return this.versions.map((version) => ({
        value: version.version_number,
        text: `#${version.version_number} · ${version.message || this.$t('workflowVersionNoMessage')}`,
      }));
    },
    canCompare() {
      return this.compareFrom > 0 && this.compareTo > 0 && this.compareFrom !== this.compareTo;
    },
  },
  watch: {
    value(open) {
      if (open) this.loadVersions();
    },
  },
  methods: {
    async loadVersions() {
      this.loading = true;
      this.error = '';
      try {
        const response = await axios.get(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/versions?count=50`,
        );
        this.versions = response.data || [];
        this.compareTo = this.latestVersionNumber;
        this.compareFrom = this.versions.length > 1
          ? this.versions[1].version_number
          : this.latestVersionNumber;
        this.comparison = null;
        this.restorePreview = null;
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.loading = false;
      }
    },
    selectForCompare(version) {
      if (this.compareFrom == null || this.compareFrom === this.compareTo) {
        this.compareFrom = version.version_number;
      } else {
        this.compareTo = version.version_number;
      }
      if (this.canCompare) this.compareVersions();
    },
    async fetchDiff(from, to) {
      const response = await axios.get(
        `/api/project/${this.projectId}/workflows/${this.workflowId}/versions/diff`
        + `?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
      );
      return response.data;
    },
    async compareVersions() {
      if (!this.canCompare) return;
      this.comparing = true;
      this.error = '';
      try {
        this.comparison = await this.fetchDiff(this.compareFrom, this.compareTo);
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.comparing = false;
      }
    },
    async previewRestore(version) {
      this.comparing = true;
      this.error = '';
      try {
        const diff = await this.fetchDiff(version.version_number, this.latestVersionNumber);
        this.restorePreview = { version, diff };
        this.restoreMessage = this.$t('workflowVersionRestoreDefaultMessage', {
          version: version.version_number,
        });
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.comparing = false;
      }
    },
    async confirmRestore() {
      if (!this.restorePreview || this.byteLength(this.restoreMessage) > 512) return;
      this.restoring = true;
      this.error = '';
      try {
        const version = this.restorePreview.version.version_number;
        const response = await axios.post(
          `/api/project/${this.projectId}/workflows/${this.workflowId}/versions/${version}/restore`,
          { message: this.restoreMessage },
        );
        this.$emit('restored', response.data);
        await this.loadVersions();
      } catch (err) {
        this.error = getErrorMessage(err);
      } finally {
        this.restoring = false;
      }
    },
    changedSections(diff) {
      const sections = (diff?.changes || []).map((change) => change.section);
      return sections.length > 0 ? sections.join(', ') : this.$t('workflowVersionNoChanges');
    },
    pretty(value) {
      return JSON.stringify(value, null, 2);
    },
    byteLength(value) {
      return new TextEncoder().encode(value || '').length;
    },
    formatCreated(value) {
      return value ? new Date(value).toLocaleString() : '—';
    },
  },
};
</script>

<style lang="scss" scoped>
.WorkflowVersionsDialog__compareControls {
  gap: 8px;

  .v-input {
    flex: 1 1 260px;
  }
}

pre {
  max-height: 280px;
  overflow: auto;
  padding: 12px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.12);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
