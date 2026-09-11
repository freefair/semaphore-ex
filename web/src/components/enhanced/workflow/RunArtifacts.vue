<template>
  <v-expansion-panels
    v-if="artifactMetadataCount > 0"
    accordion
    flat
    tile
    class="WorkflowRun__artifactPanel"
    data-testid="workflow-artifact-metadata"
  >
    <v-expansion-panel>
      <v-expansion-panel-header class="py-2">
        {{ $t('workflowArtifactRunMetadata') }} ({{ artifactMetadataCount }})
      </v-expansion-panel-header>
      <v-expansion-panel-content>
        <div class="WorkflowRun__artifactContent">
          <section v-if="fileArtifacts.length > 0">
            <div class="text-subtitle-2 mb-1">
              {{ $t('workflowFileArtifacts') }}
            </div>
            <v-list dense class="py-0">
              <v-list-item
                v-for="artifact in fileArtifacts"
                :key="`file-artifact-${artifact.id}`"
                class="px-0 WorkflowRun__fileArtifactItem"
                data-testid="workflow-file-artifact"
              >
                <v-list-item-content>
                  <v-list-item-title class="WorkflowRun__artifactTitle">
                    <strong>{{ artifact.filename }}</strong>
                    <span class="text--secondary">
                      · {{ nodeLabel(artifact.workflow_node_id) }}
                    </span>
                    <v-chip
                      x-small
                      class="ml-2"
                      :color="fileArtifactStateColor(artifact)"
                    >{{ fileArtifactStateLabel(artifact) }}</v-chip>
                  </v-list-item-title>
                  <v-list-item-subtitle class="WorkflowRun__artifactDetail">
                    {{ artifact.logical_name }} · {{ formatBytes(artifact.size_bytes) }}
                    · {{ $t('workflowFileArtifactProducer', {
                      template: artifact.producer_template_id,
                      version: artifact.producer_version,
                      task: artifact.task_id,
                      attempt: artifact.attempt,
                      user: artifact.producer_user_id,
                    }) }}
                    <span v-if="artifact.producer_runner_id">
                      · {{ $t('workflowFileArtifactRunner', {
                        runner: artifact.producer_runner_id,
                      }) }}
                    </span>
                  </v-list-item-subtitle>
                  <div class="WorkflowRun__checksum text-caption mt-1">
                    <span class="text--secondary">SHA-256</span>
                    <code class="ml-1">{{ artifact.sha256 }}</code>
                    <v-btn
                      text
                      x-small
                      color="primary"
                      class="ml-1"
                      @click="$emit('copy-checksum', artifact.sha256)"
                    >{{ $t('workflowFileArtifactCopyChecksum') }}</v-btn>
                  </div>
                  <div class="text-caption mt-1" :class="fileArtifactExpiryClass(artifact)">
                    {{ fileArtifactExpiryLabel(artifact) }}
                  </div>
                  <v-alert
                    v-if="fileArtifactDownloadErrors[artifact.id]"
                    type="warning"
                    dense
                    text
                    class="mt-2 mb-0"
                  >{{ fileArtifactDownloadErrors[artifact.id] }}</v-alert>
                </v-list-item-content>
                <v-list-item-action>
                  <v-btn
                    small
                    outlined
                    color="primary"
                    :loading="downloadingArtifactId === artifact.id"
                    :disabled="!fileArtifactDownloadable(artifact)"
                    @click="$emit('download', artifact)"
                  >
                    <v-icon left small>mdi-download</v-icon>
                    {{ $t('workflowFileArtifactDownload') }}
                  </v-btn>
                </v-list-item-action>
              </v-list-item>
            </v-list>
          </section>

          <section v-if="artifacts.length > 0">
            <div class="text-subtitle-2 mb-1">
              {{ $t('workflowArtifactOutputs') }}
            </div>
            <v-list dense class="py-0">
              <v-list-item
                v-for="artifact in artifacts"
                :key="`artifact-${artifact.workflow_node_id}-${artifact.name}`"
                class="px-0"
              >
                <v-list-item-content>
                  <v-list-item-title class="WorkflowRun__artifactTitle">
                    <strong>{{ artifact.name }}</strong>
                    <span class="text--secondary">
                      · {{ nodeLabel(artifact.workflow_node_id) }}
                    </span>
                    <v-chip
                      x-small
                      class="ml-2"
                      :color="artifactAvailabilityColor(artifact.availability)"
                    >{{ artifact.availability }}</v-chip>
                  </v-list-item-title>
                  <v-list-item-subtitle class="WorkflowRun__artifactDetail">
                    {{ schemaSummary(artifact.schema) }}
                    <span v-if="artifact.sensitive">
                      · {{ $t('workflowArtifactSensitiveRedacted') }}
                    </span>
                    <span v-else>· {{ $t('workflowArtifactNotSensitive') }}</span>
                    <span v-if="artifact.producer_task_id">
                      · {{ $t('workflowArtifactProducer', {
                        task: artifact.producer_task_id,
                        attempt: artifact.producer_attempt,
                      }) }}
                    </span>
                    <span v-if="artifact.diagnostic"> · {{ artifact.diagnostic }}</span>
                  </v-list-item-subtitle>
                </v-list-item-content>
              </v-list-item>
            </v-list>
          </section>

          <section v-if="resolvedArtifactInputs.length > 0">
            <div class="text-subtitle-2 mb-1">
              {{ $t('workflowArtifactInputs') }}
            </div>
            <v-list dense class="py-0">
              <v-list-item
                v-for="input in resolvedArtifactInputs"
                :key="`artifact-input-${input.consumer_node_id}-${input.name}`"
                class="px-0"
              >
                <v-list-item-content>
                  <v-list-item-title class="WorkflowRun__artifactTitle">
                    <strong>{{ input.name }}</strong>
                    <span class="text--secondary">
                      · {{ nodeLabel(input.consumer_node_id) }} ←
                      {{ nodeLabel(input.source_node_id) }}.{{ input.output }}
                    </span>
                    <v-chip
                      x-small
                      class="ml-2"
                      :color="artifactAvailabilityColor(input.availability)"
                    >{{ input.availability }}</v-chip>
                  </v-list-item-title>
                  <v-list-item-subtitle class="WorkflowRun__artifactDetail">
                    {{ input.required
                      ? $t('workflowArtifactRequired')
                      : $t('workflowArtifactOptional') }}
                    <span v-if="input.sensitive">
                      · {{ $t('workflowArtifactSensitiveRedacted') }}
                    </span>
                    <span v-if="input.producer_task_id">
                      · {{ $t('workflowArtifactProducer', {
                        task: input.producer_task_id,
                        attempt: input.producer_attempt,
                      }) }}
                    </span>
                  </v-list-item-subtitle>
                </v-list-item-content>
              </v-list-item>
            </v-list>
          </section>
        </div>
      </v-expansion-panel-content>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<script>
import artifactPresentation from '@/lib/enhanced/workflow-artifact-presentation';

export default {
  props: {
    workflow: Object,
    artifacts: Array,
    fileArtifacts: Array,
    resolvedArtifactInputs: Array,
    fileArtifactDownloadErrors: Object,
    downloadingArtifactId: Number,
  },
  computed: {
    artifactMetadataCount() {
      return this.fileArtifacts.length + this.artifacts.length + this.resolvedArtifactInputs.length;
    },
  },
  methods: { ...artifactPresentation },
};
</script>

<style lang="scss">
.WorkflowRun {

  &__artifactPanel {
    flex: 0 0 auto;
    border-top: 1px solid rgba(127, 127, 127, 0.2);
  }

  &__artifactContent {
    max-height: 260px;
    overflow-y: auto;
  }

  &__artifactTitle,
  &__artifactDetail {
    white-space: normal;
    overflow-wrap: anywhere;
  }

  &__checksum code {
    white-space: normal;
    overflow-wrap: anywhere;
  }
}

@media (max-width: 600px) {

  .WorkflowRun {

    &__fileArtifactItem {
      align-items: flex-start;
      flex-wrap: wrap;

      .v-list-item__action {
        align-items: flex-start;
        margin: 4px 0 8px;
        width: 100%;
      }
    }

    &__checksum {
      display: flex;
      flex-wrap: wrap;

      code {
        flex: 1 1 100%;
        margin-left: 0 !important;
      }
    }

    &__artifactContent {
      max-height: 50vh;
    }
  }
}
</style>
