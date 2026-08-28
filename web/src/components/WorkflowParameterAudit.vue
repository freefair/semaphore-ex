<template>
  <v-expansion-panels
    v-if="entryCount > 0"
    accordion
    flat
    tile
    class="WorkflowParameterAudit"
    data-testid="workflow-parameter-audit"
  >
    <v-expansion-panel>
      <v-expansion-panel-header class="py-2">
        {{ $t('workflowRunInputs') }} ({{ entryCount }})
      </v-expansion-panel-header>
      <v-expansion-panel-content>
        <v-simple-table dense>
          <tbody>
            <tr v-for="entry in parameterEntries" :key="`parameter-${entry.name}`">
              <td class="font-weight-medium">{{ entry.name }}</td>
              <td>{{ entry.snapshot.type }}</td>
              <td>{{ parameterValue(entry.snapshot) }}</td>
              <td class="text--secondary">{{ entry.snapshot.source }}</td>
            </tr>
            <tr v-for="entry in overrideEntries" :key="`override-${entry.nodeId}`">
              <td class="font-weight-medium">{{ nodeLabel(entry.nodeId) }}</td>
              <td>{{ $t('workflowNodeOverrides') }}</td>
              <td colspan="2">{{ overrideValue(entry.overrides) }}</td>
            </tr>
          </tbody>
        </v-simple-table>
      </v-expansion-panel-content>
    </v-expansion-panel>
  </v-expansion-panels>
</template>

<script>
export default {
  props: {
    details: { type: Object, required: true },
  },
  computed: {
    parameterEntries() {
      return Object.entries(this.details.run?.parameters || {})
        .sort(([left], [right]) => left.localeCompare(right))
        .map(([name, snapshot]) => ({ name, snapshot }));
    },
    overrideEntries() {
      return (this.details.nodes || [])
        .filter((entry) => entry.overrides && Object.keys(entry.overrides).length)
        .map((entry) => ({ nodeId: entry.node.id, overrides: entry.overrides }));
    },
    entryCount() {
      return this.parameterEntries.length + this.overrideEntries.length;
    },
  },
  methods: {
    parameterValue(snapshot) {
      if (snapshot.type === 'secret_reference') {
        const id = snapshot.secret_reference?.access_key_id;
        return `${this.$t ? this.$t('workflowCredential') : 'Credential'} #${id} · ${snapshot.reference_fingerprint}`;
      }
      return JSON.stringify(snapshot.value);
    },
    overrideValue(overrides) {
      const values = [];
      if (overrides.inventory_id != null) values.push(`inventory #${overrides.inventory_id}`);
      if (overrides.environment_ids != null) {
        values.push(`variable groups ${overrides.environment_ids.map((id) => `#${id}`).join(', ') || 'none'}`);
      }
      if (overrides.arguments != null) values.push(`arguments ${overrides.arguments}`);
      if (overrides.git_branch != null) values.push(`branch ${overrides.git_branch}`);
      return values.join(' · ');
    },
    nodeLabel(nodeId) {
      const entry = (this.details.nodes || []).find((node) => node.node.id === nodeId);
      return entry?.node?.display_name ? `#${nodeId} ${entry.node.display_name}` : `#${nodeId}`;
    },
  },
};
</script>

<style scoped>
.WorkflowParameterAudit {
  flex: 0 0 auto;
  border-top: 1px solid rgba(127, 127, 127, 0.2);
}
</style>
