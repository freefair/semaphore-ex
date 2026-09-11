<template>
  <span>
    <template v-for="(segment, index) in segments">
      <mark v-if="segment.match" :key="`match-${index}`"
        class="template-search-match"
      >{{ segment.text }}</mark>
      <span v-else :key="`text-${index}`">{{ segment.text }}</span>
    </template>
  </span>
</template>
<script>
export default { props: { segments: Array } };
</script>
