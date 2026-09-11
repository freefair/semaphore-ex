<template>
  <div>
  <div
    class="d-flex align-center mb-2"
    data-testid="workflow-artifact-outputs"
  >
    <span class="text-subtitle-2">{{ $t('workflowArtifactOutputs') }}</span>
    <v-spacer />
    <v-btn
      icon
      small
      :title="$t('workflowArtifactAddOutput')"
      :disabled="!canManage"
      @click="$emit('add-output')"
    >
      <v-icon small>mdi-plus</v-icon>
    </v-btn>
  </div>

  <v-card
    v-for="(output, index) in outputs"
    :key="`artifact-output-${index}`"
    outlined
    class="pa-2 mb-2"
  >
    <div class="d-flex align-start">
      <v-text-field
        :value="output.name"
        :label="$t('workflowArtifactName')"
        :disabled="!canManage"
        outlined
        dense
        hide-details="auto"
        class="mr-1 mb-3"
        @input="$emit('output-field', index, 'name', $event)"
      />
      <v-btn
        icon
        small
        :title="$t('workflowArtifactRemoveOutput')"
        :disabled="!canManage"
        @click="$emit('remove-output', index)"
      >
        <v-icon small>mdi-close</v-icon>
      </v-btn>
    </div>
    <v-select
      :value="output.schema.type"
      :items="artifactOutputTypes"
      item-value="value"
      item-text="text"
      :label="$t('workflowArtifactSchema')"
      :disabled="!canManage"
      outlined
      dense
      hide-details="auto"
      class="mb-3"
      @change="$emit('output-type', index, $event)"
    />
    <v-text-field
      :value="output.max_bytes"
      type="number"
      min="1"
      max="65536"
      :label="$t('workflowArtifactMaxBytes')"
      :disabled="!canManage"
      outlined
      dense
      hide-details="auto"
      class="mb-2"
      @input="updateOutputNumber(index, $event)"
    />
    <v-switch
      :input-value="output.sensitive"
      :label="$t('workflowArtifactSensitive')"
      :disabled="!canManage"
      dense
      hide-details
      class="mt-1"
      @change="$emit('output-field', index, 'sensitive', $event)"
    />
  </v-card>

  <div
    class="d-flex align-center mt-4 mb-2"
    data-testid="workflow-artifact-inputs"
  >
    <span class="text-subtitle-2">{{ $t('workflowArtifactInputs') }}</span>
    <v-spacer />
    <v-btn
      icon
      small
      :title="$t('workflowArtifactAddInput')"
      :disabled="!canManage || reachableArtifactOutputs.length === 0"
      @click="$emit('add-input')"
    >
      <v-icon small>mdi-plus</v-icon>
    </v-btn>
  </div>

  <div
    v-if="reachableArtifactOutputs.length === 0"
    class="text-caption text--secondary mb-3"
  >{{ $t('workflowArtifactNoReachableOutputs') }}</div>

  <v-card
    v-for="(input, index) in inputs"
    :key="`artifact-input-${index}`"
    outlined
    class="pa-2 mb-2"
  >
    <div class="d-flex align-start">
      <v-text-field
        :value="input.name"
        :label="$t('workflowArtifactInputName')"
        :disabled="!canManage"
        outlined
        dense
        hide-details="auto"
        class="mr-1 mb-3"
        @input="$emit('input-field', index, 'name', $event)"
      />
      <v-btn
        icon
        small
        :title="$t('workflowArtifactRemoveInput')"
        :disabled="!canManage"
        @click="$emit('remove-input', index)"
      >
        <v-icon small>mdi-close</v-icon>
      </v-btn>
    </div>
    <v-select
      :value="artifactReferenceKey(input)"
      :items="reachableArtifactOutputs"
      item-value="value"
      item-text="text"
      :label="$t('workflowArtifactSource')"
      :disabled="!canManage"
      outlined
      dense
      hide-details="auto"
      class="mb-2"
      @change="$emit('input-reference', index, $event)"
    />
    <v-switch
      :input-value="input.required"
      :label="$t('workflowArtifactRequired')"
      :disabled="!canManage"
      dense
      hide-details
      class="mt-1"
      @change="$emit('input-field', index, 'required', $event)"
    />
  </v-card>
  </div>
</template>

<script>
export default {
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
