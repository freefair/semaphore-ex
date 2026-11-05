<template>
  <div>
  <div
    class="d-flex align-center mb-2"
    data-testid="workflow-artifact-outputs"
  >
    <span class="text-label-large">{{ $t('workflowArtifactOutputs') }}</span>
    <v-spacer />
    <v-btn
      icon
      size="small"
      :title="$t('workflowArtifactAddOutput')"
      :disabled="!canManage"
      @click="$emit('add-output')"
    >
      <v-icon size="small">mdi-plus</v-icon>
    </v-btn>
  </div>

  <v-card
    v-for="(output, index) in outputs"
    :key="`artifact-output-${index}`"
    variant="outlined"
    class="pa-2 mb-2"
  >
    <div class="d-flex align-start">
      <v-text-field
        :model-value="output.name"
        :label="$t('workflowArtifactName')"
        :disabled="!canManage"
        variant="outlined"
        density="compact"
        hide-details="auto"
        class="mr-1 mb-3"
        @update:model-value="$emit('output-field', index, 'name', $event)"
      />
      <v-btn
        icon
        size="small"
        :title="$t('workflowArtifactRemoveOutput')"
        :disabled="!canManage"
        @click="$emit('remove-output', index)"
      >
        <v-icon size="small">mdi-close</v-icon>
      </v-btn>
    </div>
    <v-select
      :model-value="output.schema.type"
      :items="artifactOutputTypes"
      item-value="value"
      item-title="text"
      :label="$t('workflowArtifactSchema')"
      :disabled="!canManage"
      variant="outlined"
      density="compact"
      hide-details="auto"
      class="mb-3"
      @update:model-value="$emit('output-type', index, $event)"
    />
    <v-text-field
      :model-value="output.max_bytes"
      type="number"
      min="1"
      max="65536"
      :label="$t('workflowArtifactMaxBytes')"
      :disabled="!canManage"
      variant="outlined"
      density="compact"
      hide-details="auto"
      class="mb-2"
      @update:model-value="updateOutputNumber(index, $event)"
    />
    <v-switch
      :model-value="output.sensitive"
      :label="$t('workflowArtifactSensitive')"
      :disabled="!canManage"
      density="compact"
      hide-details
      class="mt-1"
      @update:model-value="$emit('output-field', index, 'sensitive', $event)"
    />
  </v-card>

  <div
    class="d-flex align-center mt-4 mb-2"
    data-testid="workflow-artifact-inputs"
  >
    <span class="text-label-large">{{ $t('workflowArtifactInputs') }}</span>
    <v-spacer />
    <v-btn
      icon
      size="small"
      :title="$t('workflowArtifactAddInput')"
      :disabled="!canManage || reachableArtifactOutputs.length === 0"
      @click="$emit('add-input')"
    >
      <v-icon size="small">mdi-plus</v-icon>
    </v-btn>
  </div>

  <div
    v-if="reachableArtifactOutputs.length === 0"
    class="text-body-small text--secondary mb-3"
  >{{ $t('workflowArtifactNoReachableOutputs') }}</div>

  <v-card
    v-for="(input, index) in inputs"
    :key="`artifact-input-${index}`"
    variant="outlined"
    class="pa-2 mb-2"
  >
    <div class="d-flex align-start">
      <v-text-field
        :model-value="input.name"
        :label="$t('workflowArtifactInputName')"
        :disabled="!canManage"
        variant="outlined"
        density="compact"
        hide-details="auto"
        class="mr-1 mb-3"
        @update:model-value="$emit('input-field', index, 'name', $event)"
      />
      <v-btn
        icon
        size="small"
        :title="$t('workflowArtifactRemoveInput')"
        :disabled="!canManage"
        @click="$emit('remove-input', index)"
      >
        <v-icon size="small">mdi-close</v-icon>
      </v-btn>
    </div>
    <v-select
      :model-value="artifactReferenceKey(input)"
      :items="reachableArtifactOutputs"
      item-value="value"
      item-title="text"
      :label="$t('workflowArtifactSource')"
      :disabled="!canManage"
      variant="outlined"
      density="compact"
      hide-details="auto"
      class="mb-2"
      @update:model-value="$emit('input-reference', index, $event)"
    />
    <v-switch
      :model-value="input.required"
      :label="$t('workflowArtifactRequired')"
      :disabled="!canManage"
      density="compact"
      hide-details
      class="mt-1"
      @update:model-value="$emit('input-field', index, 'required', $event)"
    />
  </v-card>
  </div>
</template>

<script>
export default {
  emits: [
    'add-input', 'add-output', 'input-field', 'input-reference', 'output-field',
    'output-type', 'remove-input', 'remove-output',
  ],
  props: {
    outputs: Array,
    inputs: Array,
    artifactOutputTypes: Array,
    reachableArtifactOutputs: Array,
    artifactReferenceKey: Function,
    canManage: Boolean,
  },
  methods: {
    updateOutputNumber(index, value) {
      const number = parseFloat(value);
      this.$emit('output-field', index, 'max_bytes', Number.isNaN(number) ? value : number);
    },
  },
};
</script>
