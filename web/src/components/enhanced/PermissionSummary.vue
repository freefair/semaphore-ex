<template>
  <div class="PermissionSummary" data-testid="permission-summary">
    <span
      v-if="summary.resources.length === 0 && summary.others.length === 0"
      class="text-medium-emphasis"
      data-testid="permission-summary-none"
    >{{ $t('permNone') }}</span>

    <div
      v-for="row in summary.resources"
      :key="row.resource"
      class="PermissionSummary__resource"
      :data-testid="`permission-summary-${row.resource}`"
    >
      <span class="PermissionSummary__resourceName">
        {{ $t(RESOURCE_LABEL_KEYS[row.resource]) }}:
      </span>
      {{ row.actions.map((action) => $t(ACTION_LABEL_KEYS[action])).join(' · ') }}
    </div>

    <div v-if="summary.others.length > 0" class="PermissionSummary__others">
      <v-chip
        v-for="definition in summary.others"
        :key="definition.id"
        size="small"
        variant="tonal"
        :data-testid="`permission-summary-other-${definition.id}`"
      >{{ definition.description }}</v-chip>
    </div>
  </div>
</template>

<script>
import {
  ACTION_LABEL_KEYS, RESOURCE_LABEL_KEYS, permissionSummary,
} from '@/lib/enhanced/project-access';

export default {
  props: {
    permissionIds: { type: Array, default: () => [] },
    catalog: { type: Array, default: () => [] },
  },
  data() {
    return { ACTION_LABEL_KEYS, RESOURCE_LABEL_KEYS };
  },
  computed: {
    summary() {
      return permissionSummary(this.permissionIds, this.catalog);
    },
  },
};
</script>

<style scoped>
.PermissionSummary {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 13px;
}

.PermissionSummary__resourceName {
  font-weight: 500;
}

.PermissionSummary__others {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin-top: 2px;
}
</style>
