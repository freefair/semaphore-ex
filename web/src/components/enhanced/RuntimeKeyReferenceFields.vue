<template>
  <div>
    <v-text-field
      v-if="active"
      v-model="mountModel"
      label="KV v2 mount"
      :rules="[(v) => !!v || 'Mount is required']"
      :disabled="disabled"
      data-testid="key-runtimeMount"
      outlined
      dense
    />

    <slot />

    <v-row
      v-if="active"
    >
      <v-col cols="12" sm="5">
        <v-text-field
          v-model.number="versionModel"
          label="Version (optional)"
          type="number"
          min="0"
          :rules="[(v) => v == null || v >= 0 || 'Version must not be negative']"
          :disabled="disabled"
          data-testid="key-runtimeVersion"
          outlined
          dense
        />
      </v-col>
      <v-col cols="12" sm="7">
        <v-text-field
          v-model="fieldModel"
          label="Field"
          :rules="[(v) => !!v || 'Field is required']"
          :disabled="disabled"
          data-testid="key-runtimeField"
          outlined
          dense
        />
      </v-col>
    </v-row>
  </div>
</template>

<script>
export default {
  props: {
    active: Boolean, disabled: Boolean, mount: String, version: [Number, String], field: String,
  },
  computed: {
    mountModel: {
      get() { return this.mount; },
      set(value) { this.$emit('update:mount', value); },
    },
    versionModel: {
      get() { return this.version; },
      set(value) { this.$emit('update:version', value); },
    },
    fieldModel: {
      get() { return this.field; },
      set(value) { this.$emit('update:field', value); },
    },
  },
};
</script>
