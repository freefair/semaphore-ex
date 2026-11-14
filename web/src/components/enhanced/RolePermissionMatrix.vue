<template>
  <div class="RolePermissionMatrix" data-testid="role-permission-matrix">
    <v-table v-if="matrix.rows.length > 0" density="compact" class="RolePermissionMatrix__table">
      <thead>
        <tr>
          <th>{{ $t('permMatrixResource') }}</th>
          <th v-for="action in columns" :key="action" class="text-center">
            {{ $t(ACTION_LABEL_KEYS[action]) }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="row in matrix.rows"
          :key="row.resource"
          :data-testid="`role-matrix-${row.resource}`"
        >
          <th scope="row" class="RolePermissionMatrix__resource">
            {{ $t(RESOURCE_LABEL_KEYS[row.resource]) }}
          </th>
          <td v-for="action in columns" :key="action" class="text-center">
            <v-checkbox-btn
              v-if="cell(row, action)"
              class="d-inline-flex"
              :model-value="selected.has(cell(row, action).id)"
              :disabled="disabled"
              :aria-label="cellLabel(row, action)"
              :data-testid="`role-perm-${cell(row, action).id}`"
              @update:model-value="toggle(cell(row, action).id, $event)"
            />
          </td>
        </tr>
      </tbody>
    </v-table>

    <template v-if="matrix.other.length > 0">
      <v-list-subheader class="pl-0 mt-2">{{ $t('permOtherPermissions') }}</v-list-subheader>
      <v-checkbox
        v-for="definition in matrix.other"
        :key="definition.id"
        class="mt-0"
        density="compact"
        hide-details
        :model-value="selected.has(definition.id)"
        :label="definition.description"
        :disabled="disabled"
        :data-testid="`role-perm-${definition.id}`"
        @update:model-value="toggle(definition.id, $event)"
      />
    </template>
  </div>
</template>

<script>
import {
  ACTION_LABEL_KEYS, RESOURCE_LABEL_KEYS, resourceMatrix, togglePermissionId,
} from '@/lib/enhanced/project-access';

export default {
  props: {
    catalog: { type: Array, default: () => [] },
    modelValue: { type: Array, default: () => [] },
    disabled: Boolean,
  },
  emits: ['update:modelValue'],
  data() {
    return { ACTION_LABEL_KEYS, RESOURCE_LABEL_KEYS };
  },
  computed: {
    matrix() {
      return resourceMatrix(this.catalog);
    },
    columns() {
      const actions = ['view', 'create', 'update', 'delete', 'use'];
      return actions.filter((action) => this.matrix.rows.some((row) => this.cell(row, action)));
    },
    selected() {
      return new Set(this.modelValue);
    },
  },
  methods: {
    cellLabel(row, action) {
      return `${this.$t(RESOURCE_LABEL_KEYS[row.resource])}: ${this.$t(ACTION_LABEL_KEYS[action])}`;
    },
    cell(row, action) {
      return row.cells.find((definition) => definition.id.endsWith(`.${action}`)) || null;
    },
    toggle(id, enabled) {
      this.$emit('update:modelValue', togglePermissionId(this.modelValue, id, enabled));
    },
  },
};
</script>

<style scoped>
.RolePermissionMatrix__resource {
  font-weight: 500;
  white-space: nowrap;
}
</style>
