<template>
  <div class="d-flex justify-end align-center mt-3" data-testid="task-summary-pagination">
    <v-btn
      size="small"
      variant="text"
      :disabled="!hasPrevious || loading"
      @click="$emit('previous')"
    >
      <v-icon start size="small">mdi-chevron-left</v-icon>Previous
    </v-btn>
    <v-btn size="small" variant="text" :disabled="!hasNext || loading" @click="$emit('next')">
      Next<v-icon end size="small">mdi-chevron-right</v-icon>
    </v-btn>
  </div>
</template>

<script>
export default {
  props: {
    hasPrevious: Boolean,
    hasNext: Boolean,
    loading: Boolean,
  },
  emits: ['previous', 'next'],
};
</script>
