<template>
  <v-dialog v-model="valueModel" max-width="700">
    <v-card v-if="generatedKeyResult" class="generated-ssh-key-result">
      <v-card-title>SSH public key</v-card-title>
      <v-card-text>
        <v-alert dense text type="info">
          The private key is stored encrypted by Semaphore and is never displayed.
        </v-alert>
        <div>Algorithm: <code>{{ generatedKeyResult.algorithm }}</code></div>
        <div class="generated-ssh-key-fingerprint">
          Fingerprint: <code>{{ generatedKeyResult.fingerprint }}</code>
        </div>
        <div style="position: relative">
          <pre
            class="pa-2 mt-3 generated-ssh-key-public"
            style="overflow: auto; background: #616161; color: white; border-radius: 6px"
          >{{ generatedKeyResult.public_key }}</pre>
          <CopyClipboardButton
            style="position: absolute; right: 0; top: 0; transform: scale(0.9);"
            :text="generatedKeyResult.public_key"
          />
        </div>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn text color="primary" @click="$emit('input', false)">Close</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script>
import CopyClipboardButton from '@/components/CopyClipboardButton.vue';

export default {
  components: { CopyClipboardButton },
  props: {
    value: Boolean,
    generatedKeyResult: Object,
  },
  computed: {
    valueModel: {
      get() { return this.value; },
      set(value) { this.$emit('input', value); },
    },
  },
};
</script>

<style lang="scss" scoped>
@import './ssh-key-presentation';
</style>
