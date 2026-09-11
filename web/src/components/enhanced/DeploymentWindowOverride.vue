<template>
  <div>
    <v-alert
      v-if="block"
      type="warning"
      text
      dense
      data-testid="deployment-window-blocked"
    >
      <div>{{ message }}</div>
      <div
        v-if="block.next_eligible_known
          && block.next_eligible_at"
        class="text-caption mt-1"
      >
        {{ $t('deploymentWindowNextEligible') }}:
        {{ formatDate(block.next_eligible_at) }}
      </div>
    </v-alert>

    <template v-if="block">
      <div class="text-subtitle-2 mb-2">{{ $t('deploymentWindowEmergencyOverride') }}</div>
      <v-select
        v-model="categoryModel"
        :items="categories"
        :label="$t('deploymentWindowOverrideCategory')"
        outlined
        dense
        :disabled="disabled"
      />
      <v-text-field
        v-model.trim="referenceModel"
        :label="$t('deploymentWindowOverrideReference')"
        :hint="$t('deploymentWindowOverrideReferenceHint')"
        persistent-hint
        outlined
        dense
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
