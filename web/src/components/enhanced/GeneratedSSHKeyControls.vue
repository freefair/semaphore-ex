<template>
  <div>
  <v-checkbox
    v-if="canGenerate"
    v-model="generateModel"
    label="Generate on server"
    :disabled="disabled"
    data-testid="key-generateServer"
  />

  <v-select
    v-if="canGenerate && generate"
    v-model="algorithmModel"
    :items="algorithms"
    item-value="id"
    item-text="name"
    label="Key algorithm"
    :disabled="disabled"
    data-testid="key-generateAlgorithm"
    outlined
    dense
  />

  <v-alert
    v-if="metadata"
    dense
    text
    type="info"
    data-testid="key-generatedMetadata"
    class="generated-ssh-key-metadata"
  >
    <div><strong>Generated {{ metadata.algorithm }}</strong></div>
    <div class="generated-ssh-key-fingerprint">
      Fingerprint: <code>{{ metadata.fingerprint }}</code>
    </div>
    <div style="position: relative">
      <pre
        class="pa-2 mt-2 generated-ssh-key-public"
        style="overflow: auto; background: #616161; color: white; border-radius: 6px"
      >{{ metadata.public_key }}</pre>
      <CopyClipboardButton
        style="position: absolute; right: 0; top: 0; transform: scale(0.9);"
        :text="metadata.public_key"
      />
    </div>
    The private key is stored encrypted by Semaphore and is never displayed.
  </v-alert>
  </div>
</template>

<script>
import CopyClipboardButton from '@/components/CopyClipboardButton.vue';

export default {
  components: { CopyClipboardButton },
  props: {
    canGenerate: Boolean,
    generate: Boolean,
    algorithm: String,
    algorithms: Array,
    metadata: Object,
    disabled: Boolean,
  },
  computed: {
    generateModel: {
      get() { return this.generate; },
      set(value) { this.$emit('update:generate', value); },
    },
    algorithmModel: {
      get() { return this.algorithm; },
      set(value) { this.$emit('update:algorithm', value); },
    },
  },
};
</script>

<style lang="scss" scoped>
@import './ssh-key-presentation';
</style>
