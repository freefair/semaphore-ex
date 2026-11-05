<template>
  <div>
    <v-alert v-if="error" density="compact" type="error">{{ error }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate class="mb-2" />
    <SSHKeyBindingsEditor
      :model-value="modelValue"
      :keys="keys"
      :inherited="inheritedKeys"
      :always="project.always_ssh_keys || []"
      :inherit-label="template ? $t('taskSSHKeysInheritTemplate') : $t('taskSSHKeysInheritProject')"
      :disabled="disabled || loading || !!error"
      @update:model-value="$emit('update:modelValue', $event)"
    />
  </div>
</template>

<script>
import axios from 'axios';
import SSHKeyBindingsEditor from '@/components/enhanced/SSHKeyBindingsEditor.vue';
import { getErrorMessage } from '@/lib/error';

export default {
  emits: ['update:modelValue'],
  components: { SSHKeyBindingsEditor },
  props: {
    projectId: { type: [Number, String], required: true },
    modelValue: { type: Array, default: null },
    template: { type: Object, default: null },
    disabled: Boolean,
  },
  data: () => ({
    keys: [], project: {}, loading: false, error: null, requestId: 0,
  }),
  computed: {
    inheritedKeys() {
      return this.template?.ssh_keys ?? this.project.default_ssh_keys ?? [];
    },
  },
  watch: {
    projectId: { immediate: true, handler: 'load' },
  },
  methods: {
    async load() {
      const requestId = this.requestId + 1;
      this.requestId = requestId;
      this.loading = true;
      this.error = null;
      this.keys = [];
      this.project = {};
      try {
        const [keys, project] = await Promise.all([
          axios.get(`/api/project/${this.projectId}/keys`),
          axios.get(`/api/project/${this.projectId}`),
        ]);
        if (requestId !== this.requestId) return;
        this.keys = keys.data.filter((key) => key.type === 'ssh');
        this.project = project.data;
      } catch (error) {
        if (requestId === this.requestId) this.error = getErrorMessage(error);
      } finally {
        if (requestId === this.requestId) this.loading = false;
      }
    },
  },
};
</script>
