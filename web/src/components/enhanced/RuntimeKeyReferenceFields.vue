<template>
  <div>
    <v-text-field
      v-if="active"
      v-model="mountModel"
      label="KV v2 mount"
      :rules="[(v) => !!v || 'Mount is required']"
      :disabled="disabled"
      data-testid="key-runtimeMount"
      variant="outlined"
      density="compact"
    />

    <slot />

    <v-row
      v-if="active"
    >
      <v-col cols="12">
        <v-text-field
          v-model="fieldModel"
          label="Field"
          :rules="[(v) => !!v || 'Field is required']"
          :disabled="disabled"
          data-testid="key-runtimeField"
          variant="outlined"
          density="compact"
        />
      </v-col>
    </v-row>
  </div>
</template>

<script>
export default {
  emits: ['update:field', 'update:mount'],
  props: {
    active: Boolean, disabled: Boolean, mount: String, field: String,
  },
  computed: {
    mountModel: {
      get() { return this.mount; },
      set(value) { this.$emit('update:mount', value); },
    },
    fieldModel: {
      get() { return this.field; },
      set(value) { this.$emit('update:field', value); },
    },
  },
};
</script>
