<template>
  <div>
    <div class="template-search-bar px-4 pt-4">
      <v-text-field
        ref="templateSearch"
        v-model="inputModel"
        :label="$t('templateSearchLabel')"
        prepend-inner-icon="mdi-magnify"
        clearable
        dense
        outlined
        hide-details
        maxlength="256"
        :loading="loading"
        data-testid="template-search"
        @input="$emit('input', $event)"
        @click:clear="$emit('clear')"
        @keydown.esc.stop.prevent="$emit('clear')"
      />
      <div
        v-if="appliedSearch && !loading"
        class="template-search-results"
        data-testid="template-search-results"
        aria-live="polite"
      >
        {{ $t('templateSearchResultCount', { count: count }) }}
      </div>
    </div>

    <v-alert
      v-if="error"
      dense
      text
      type="error"
      class="mx-4 mt-3 mb-0"
    >
      {{ error }}
    </v-alert>

  </div>
</template>
<script>
export default {
  props: {
    value: String, appliedSearch: String, loading: Boolean, count: Number, error: String,
  },
  computed: {
    inputModel: {
      get() { return this.value; },
      set(value) { this.$emit('update:value', value); },
    },
  },
  methods: {
    focus() { this.$refs.templateSearch.focus(); },
  },
};
</script>
