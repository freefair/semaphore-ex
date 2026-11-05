<template>
  <section class="mt-6">
    <h3 class="mb-2">{{ $t('taskSSHKeys') }}</h3>
    <p class="text-body-medium">{{ $t('taskSSHKeysDescription') }}</p>
    <v-alert v-if="error" density="compact" type="error">{{ error }}</v-alert>
    <v-progress-linear v-if="loading" indeterminate class="mb-2" />
    <SSHKeyBindingsEditor
      :model-value="project.default_ssh_keys || []"
      :keys="keys"
      :title="$t('taskSSHKeysDefaults')"
      :description="$t('taskSSHKeysDefaultsHint')"
      :disabled="disabled || loading || !!error"
      @update:model-value="$emit('defaults', $event)"
    />
    <SSHKeyBindingsEditor
      :model-value="project.always_ssh_keys || []"
      :keys="keys"
      :title="$t('taskSSHKeysAlways')"
      :description="$t('taskSSHKeysAlwaysHint')"
      :disabled="disabled || loading || !!error"
      @update:model-value="$emit('always', $event)"
    />
  </section>
</template>

<script>
import axios from 'axios';
import SSHKeyBindingsEditor from '@/components/enhanced/SSHKeyBindingsEditor.vue';
import { getErrorMessage } from '@/lib/error';

export default {
  emits: ['always', 'defaults'],
  components: { SSHKeyBindingsEditor },
  props: {
    project: { type: Object, required: true },
    disabled: Boolean,
  },
  data: () => ({ keys: [], loading: false, error: null }),
  async created() {
    this.loading = true;
    try {
      const response = await axios.get(`/api/project/${this.project.id}/keys`);
      this.keys = response.data.filter((key) => key.type === 'ssh');
    } catch (error) {
      this.error = getErrorMessage(error);
    } finally {
      this.loading = false;
    }
  },
};
</script>
