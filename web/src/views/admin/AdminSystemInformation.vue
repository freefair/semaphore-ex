<template>
  <div data-testid="admin-system-information">
    <v-toolbar flat>
      <v-toolbar-title>{{ $t('adminSystemInformation') }}</v-toolbar-title>
    </v-toolbar>

    <div class="px-4 pb-8">
      <v-progress-linear v-if="loading" indeterminate />
      <v-alert v-else-if="error" type="error" variant="outlined" density="compact">
        {{ error }}
      </v-alert>
      <SystemInfoContent v-else-if="info" :info="info" :system-info="systemInfo" />
    </div>
  </div>
</template>

<script>
import axios from 'axios';
import SystemInfoContent from '@/components/SystemInfoContent.vue';

export default {
  components: { SystemInfoContent },

  props: {
    systemInfo: Object,
  },

  data() {
    return { info: null, loading: false, error: null };
  },

  async created() {
    this.loading = true;
    try {
      this.info = (await axios.get('/api/admin/info')).data;
    } catch (err) {
      this.error = err.response?.data?.message || err.message;
    } finally {
      this.loading = false;
    }
  },
};
</script>
