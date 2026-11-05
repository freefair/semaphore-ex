<template>
  <fieldset class="ssh-key-bindings mb-4" :disabled="disabled">
    <legend class="px-1">{{ title || $t('taskSSHKeys') }}</legend>
    <p v-if="description" class="text-body-medium mt-2 mb-2">{{ description }}</p>
    <v-checkbox
      v-if="inheritLabel"
      :model-value="modelValue == null"
      :label="inheritLabel"
      :disabled="disabled"
      class="mt-1"
      hide-details
      @update:model-value="setInherited"
    />
    <div v-if="inheritLabel && modelValue == null" class="mt-3">
      <p v-if="!inherited.length" class="text-body-medium">{{ $t('taskSSHKeysEmpty') }}</p>
      <div v-for="binding in inherited" :key="binding.access_key_id" class="text-body-medium mb-2">
        <strong>{{ keyName(binding.access_key_id) }}</strong>: {{ hostLabel(binding) }}
      </div>
    </div>
    <template v-else>
      <div v-for="(binding, index) in modelValue || []" :key="index" class="mt-3">
        <div class="d-flex align-start">
          <v-autocomplete
            :model-value="binding.access_key_id"
            :items="keys"
            item-value="id"
            item-title="name"
            :label="$t('taskSSHKey')"
            :rules="[v => !!v || $t('taskSSHKeyRequired')]"
            :disabled="disabled"
            variant="outlined"
            density="compact"
            @update:model-value="updateBinding(index, { access_key_id: $event })"
          />
          <v-btn
            icon
            :disabled="disabled"
            :aria-label="$t('taskSSHKeyRemove')"
            @click="removeBinding(index)"
          ><v-icon>mdi-close</v-icon></v-btn>
        </div>
        <v-combobox
          :model-value="binding.hosts || []"
          :label="$t('taskSSHHosts')"
          :hint="$t('taskSSHHostsHint')"
          :delimiters="[',', ' ']"
          :disabled="disabled"
          multiple

          closable-chips
          persistent-hint
          variant="outlined"
          density="compact"
          @update:model-value="updateBinding(index, { hosts: $event })"
        />
      </div>
      <v-btn class="mt-2 mb-2" size="small" variant="text" :disabled="disabled" @click="addBinding">
        <v-icon start size="small">mdi-plus</v-icon>{{ $t('taskSSHKeyAdd') }}
      </v-btn>
    </template>
    <div v-if="always.length" class="mt-3 text-body-medium">
      <strong>{{ $t('taskSSHKeysAlways') }}</strong>
      <div v-for="binding in always" :key="binding.access_key_id" class="mt-1">
        {{ keyName(binding.access_key_id) }}: {{ hostLabel(binding) }}
      </div>
      <p class="mt-1 mb-0">{{ $t('taskSSHKeysAlwaysHint') }}</p>
    </div>
  </fieldset>
</template>

<script>
function copyBindings(bindings) {
  return (bindings || []).map((binding) => ({
    access_key_id: binding.access_key_id,
    hosts: [...new Set((binding.hosts || []).map((host) => String(host).trim().toLowerCase())
      .filter(Boolean))],
  }));
}

export default {
  emits: ['update:modelValue'],
  props: {
    modelValue: { type: Array, default: null },
    keys: { type: Array, default: () => [] },
    inherited: { type: Array, default: () => [] },
    always: { type: Array, default: () => [] },
    inheritLabel: { type: String, default: '' },
    title: { type: String, default: '' },
    description: { type: String, default: '' },
    disabled: Boolean,
  },
  methods: {
    hostLabel(binding) {
      return binding.hosts?.length ? binding.hosts.join(', ') : this.$t('taskSSHHostsUnmapped');
    },
    keyName(id) {
      return this.keys.find((key) => key.id === id)?.name || this.$t('taskSSHKeyMissing', { id });
    },
    setInherited(enabled) {
      if (!this.disabled) this.$emit('update:modelValue', enabled ? null : copyBindings(this.inherited));
    },
    addBinding() {
      if (this.disabled) return;
      this.$emit('update:modelValue', [...copyBindings(this.modelValue), { access_key_id: null, hosts: [] }]);
    },
    removeBinding(index) {
      if (this.disabled) return;
      this.$emit('update:modelValue', copyBindings(this.modelValue).filter((binding, itemIndex) => itemIndex !== index));
    },
    updateBinding(index, update) {
      if (this.disabled) return;
      const bindings = copyBindings(this.modelValue);
      bindings[index] = { ...bindings[index], ...update };
      this.$emit('update:modelValue', copyBindings(bindings));
    },
  },
};
</script>

<style scoped>
.ssh-key-bindings {
  border: 1px solid rgba(128, 128, 128, 0.4);
  border-radius: 4px;
  padding: 8px 12px 12px;
  min-width: 0;
}
</style>
