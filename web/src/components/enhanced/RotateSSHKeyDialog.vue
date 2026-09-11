<template>
  <v-dialog v-model="valueModel" max-width="640">
    <v-card v-if="rotationItem" class="generated-ssh-key-rotation">
      <v-card-title>Rotate SSH key</v-card-title>
      <v-card-text>
        <v-alert dense text type="warning">
          Update every authorized_keys destination with the new public key immediately.
          Running jobs keep their already loaded key.
        </v-alert>
        <ObjectRefsView
          object-title="access key"
          :object-refs="rotationRefs"
          :project-id="projectId"
          hide-warning
        />
        <v-select
          v-model="rotationAlgorithmModel"
          :items="algorithms"
          item-value="id"
          item-text="name"
          label="Key algorithm"
          outlined
          dense
          :disabled="rotationLoading"
        />
        <v-alert v-if="rotationError" dense text type="error">{{ rotationError }}</v-alert>
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn text :disabled="rotationLoading" @click="$emit('input', false)">
          Cancel
        </v-btn>
        <v-btn
          color="primary"
          :loading="rotationLoading"
          :disabled="rotationLoading"
          @click="$emit('rotate')"
        >
          Rotate key
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script>
import ObjectRefsView from '@/components/ObjectRefsView.vue';

export default {
  components: { ObjectRefsView },
  props: {
    value: Boolean,
    rotationItem: Object,
    rotationRefs: Object,
    projectId: Number,
    rotationAlgorithm: String,
    algorithms: Array,
    rotationLoading: Boolean,
    rotationError: String,
  },
  computed: {
    valueModel: {
      get() { return this.value; },
      set(value) { this.$emit('input', value); },
    },
    rotationAlgorithmModel: {
      get() { return this.rotationAlgorithm; },
      set(value) { this.$emit('update:rotationAlgorithm', value); },
    },
  },
};
</script>

<style lang="scss" scoped>
@import './ssh-key-presentation';
</style>
