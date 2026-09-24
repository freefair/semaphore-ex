<template>
  <div>
    <v-toolbar flat>
      <v-app-bar-nav-icon @click="showDrawer" />
      <v-toolbar-title>{{ $t('hostsTitle') }}</v-toolbar-title>
    </v-toolbar>
    <v-divider />
    <HostBrowser :key="projectId" :project-id="projectId" class="pa-4" />
  </div>
</template>
<script>
import EventBus from '@/event-bus';
import HostBrowser from '@/components/enhanced/HostBrowser.vue';

export default {
  components: { HostBrowser },
  props: { projectId: Number },
  methods: {
    showDrawer() {
      EventBus.$emit('i-show-drawer');
    },
  },
};
</script>
