<template>
  <div>
    <v-alert
      v-if="block"
      type="warning"
      variant="tonal"
      density="compact"
      data-testid="deployment-window-blocked"
    >
      <div>{{ message }}</div>
      <div
        v-if="block.next_eligible_known
          && block.next_eligible_at"
        class="text-body-small mt-1"
      >
        {{ $t('deploymentWindowNextEligible') }}:
        {{ formatDate(block.next_eligible_at) }}
      </div>
    </v-alert>

    <template v-if="block">
      <div class="text-label-large mb-2">{{ $t('deploymentWindowEmergencyOverride') }}</div>
      <v-select
        v-model="categoryModel"
        :items="categories"
        item-title="text"
        :label="$t('deploymentWindowOverrideCategory')"
        variant="outlined"
        density="compact"
        :disabled="disabled"
      />
      <v-text-field
        v-model.trim="referenceModel"
        :label="$t('deploymentWindowOverrideReference')"
        :hint="$t('deploymentWindowOverrideReferenceHint')"
        persistent-hint
        variant="outlined"
        density="compact"
        :disabled="disabled"
        data-testid="deployment-window-override-reference"
      />
      <v-checkbox
        v-model="confirmedModel"
        :label="$t('deploymentWindowOverrideConfirm')"
        :disabled="disabled"
        data-testid="deployment-window-override-confirm"
      />
    </template>

  </div>
</template>

<script>
export default {
  emits: ['update:category', 'update:confirmed', 'update:reference'],
  props: {
    block: Object,
    message: String,
    categories: Array,
    category: String,
    reference: String,
    confirmed: Boolean,
    disabled: Boolean,
    formatDate: Function,
  },
  computed: {
    categoryModel: {
      get() { return this.category; },
      set(value) { this.$emit('update:category', value); },
    },
    referenceModel: {
      get() { return this.reference; },
      set(value) { this.$emit('update:reference', value); },
    },
    confirmedModel: {
      get() { return this.confirmed; },
      set(value) { this.$emit('update:confirmed', value); },
    },
  },
};
</script>
