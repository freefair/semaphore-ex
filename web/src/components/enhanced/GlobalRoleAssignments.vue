<template>
  <div>
  <v-alert :value="globalRoleError" color="error" class="pb-2">
    {{ globalRoleError }}
  </v-alert>

  <v-row align="center">
    <v-col cols="12" sm="8">
      <v-select
        v-model="newGlobalRoleIdModel"
        :items="availableGlobalRoles"
        item-value="id"
        item-text="name"
        label="Global role"
        outlined
        dense
        hide-details
        :disabled="globalRolesLoading"
        data-testid="global-role-select"
      />
    </v-col>
    <v-col cols="12" sm="4">
      <v-btn
        block
        color="primary"
        :disabled="!newGlobalRoleId || globalRolesLoading"
        :loading="globalRolesLoading"
        @click="$emit('assign')"
      >
        Assign
      </v-btn>
    </v-col>
  </v-row>

  <v-list v-if="globalRoleAssignments.length > 0" class="px-0">
    <v-list-item
      v-for="assignment in globalRoleAssignments"
      :key="assignment.id"
      class="px-0"
    >
      <v-list-item-content>
        <v-list-item-title>{{ assignment.role_name }}</v-list-item-title>
        <TemplatePermissionsChips
          class="pt-1"
          scope="global"
          :permissions="assignment.global_permissions || 0"
        />
      </v-list-item-content>
      <v-list-item-action>
        <v-btn
          icon
          :disabled="globalRolesLoading"
          :aria-label="`Remove ${assignment.role_name}`"
          @click="$emit('remove', assignment)"
        >
          <v-icon>mdi-delete</v-icon>
        </v-btn>
      </v-list-item-action>
    </v-list-item>
  </v-list>
  <v-alert v-else text dense type="info" class="mt-4">
    No global roles assigned.
  </v-alert>

  <v-subheader class="px-0 mt-4">Effective global permissions</v-subheader>
  <v-list dense class="px-0">
    <v-list-item
      v-for="decision in effectiveGlobalPermissions.decisions || []"
      :key="decision.permission"
      class="px-0"
    >
      <v-list-item-icon class="mr-3">
        <v-icon :color="decision.allowed ? 'success' : 'grey'">
          {{ decision.allowed ? 'mdi-check-circle' : 'mdi-minus-circle-outline' }}
        </v-icon>
      </v-list-item-icon>
      <v-list-item-content>
        <v-list-item-title>
          {{ globalPermissionDescription(decision.permission) }}
        </v-list-item-title>
        <v-list-item-subtitle>
          {{ globalPermissionProvenance(decision) }}
        </v-list-item-subtitle>
      </v-list-item-content>
    </v-list-item>
  </v-list>
  </div>
</template>

<script>
import TemplatePermissionsChips from '@/components/TemplatePermissionsChips.vue';

export default {
  components: { TemplatePermissionsChips },
  props: {
    globalRoleError: String,
    newGlobalRoleId: [String, Number],
    availableGlobalRoles: Array,
    globalRolesLoading: Boolean,
    globalRoleAssignments: Array,
    effectiveGlobalPermissions: Object,
    globalPermissionDescription: Function,
    globalPermissionProvenance: Function,
  },
  computed: {
    newGlobalRoleIdModel: {
      get() { return this.newGlobalRoleId; },
      set(value) { this.$emit('update:newGlobalRoleId', value); },
    },
  },
};
</script>
