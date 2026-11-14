<template>
  <div class="empty-picker-hint" :data-testid="testId">
    <span>{{ text }}</span>
    <router-link :to="to">{{ linkText }}</router-link>
  </div>
</template>

<script>
// Shown inside a picker that has nothing to choose yet, so the next step is
// visible instead of a dead end.
export default {
  props: {
    text: { type: String, required: true },
    linkText: { type: String, required: true },
    to: { type: String, required: true },
    testId: String,
  },
};
</script>

<style scoped>
.empty-picker-hint {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding: 8px 16px;
}
</style>
