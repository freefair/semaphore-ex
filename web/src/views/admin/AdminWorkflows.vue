<template>
  <div data-testid="admin-workflows">
    <v-toolbar flat>
      <v-toolbar-title>{{ $t('adminWorkflows') }}</v-toolbar-title>
    </v-toolbar>

    <div class="px-4 pb-8">
      <WorkflowArtifactRetentionPanel />
    </div>
  </div>
</template>

<script>
import WorkflowArtifactRetentionPanel from '@/components/WorkflowArtifactRetentionPanel.vue';

export default {
  components: { WorkflowArtifactRetentionPanel },
};
</script>
