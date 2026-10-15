<template>
  <v-card v-if="plan" outlined class="execution-review" data-testid="execution-preflight-review">
    <v-card-title class="text-subtitle-1 py-3">
      <v-icon small class="mr-2">mdi-clipboard-check-outline</v-icon>
      {{ $t('executionPreflightReview') }}
    </v-card-title>
    <v-card-text class="pt-0">
      <v-alert
        v-for="(finding, index) in visibleFindings"
        :key="`${finding.code}-${finding.node_id || 0}-${index}`"
        :type="findingType(finding.severity)"
        dense
        text
        class="mb-2"
        :data-testid="`execution-preflight-${finding.severity}`"
      >
        <div>{{ finding.message }}</div>
        <div v-if="policyFindingLabel(finding)" class="text-caption mt-1">
          <code>{{ policyFindingLabel(finding) }}</code>
        </div>
        <a
          v-if="safeRemediationURL(finding.remediation_url)"
          :href="safeRemediationURL(finding.remediation_url)"
          target="_blank"
          rel="noopener noreferrer"
          class="text-caption"
        >Remediation</a>
      </v-alert>

      <div class="text-caption text--secondary">{{ $t('executionDefinition') }}</div>
      <div class="mb-3 font-weight-medium">
        {{ plan.definition.name || `#${plan.definition.id}` }}
      </div>

      <div v-if="plan.inputs && plan.inputs.length" class="mb-3">
        <div class="text-caption text--secondary mb-1">{{ $t('effectiveInputs') }}</div>
        <v-chip
          v-for="input in plan.inputs"
          :key="input.name"
          small
          outlined
          class="mr-1 mb-1"
        >
          <v-icon v-if="input.sensitive" x-small left>mdi-lock-outline</v-icon>
          {{ input.name }} · {{ input.source }} ·
          {{ input.present ? $t('present') : $t('missing') }}
        </v-chip>
      </div>

      <div v-if="plan.references && plan.references.length" class="mb-3">
        <div class="text-caption text--secondary mb-1">{{ $t('resolvedReferences') }}</div>
        <table class="execution-references text-body-2">
          <thead>
            <tr>
              <th scope="col">{{ $t('executionReferenceName') }}</th>
              <th scope="col">{{ $t('executionReferenceBinding') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="reference in plan.references" :key="referenceKey(reference)">
              <td>
                <v-icon v-if="!reference.visible" x-small>mdi-eye-off-outline</v-icon>
                {{ reference.name || `#${reference.id}` }}
                <div class="text-caption text--secondary">{{ reference.kind }}</div>
              </td>
              <td>{{ reference.binding_target || '—' }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="plan.placements && plan.placements.length" class="mb-3">
        <div class="text-caption text--secondary mb-1">{{ $t('runnerSelection') }}</div>
        <div
          v-for="(placement, index) in plan.placements"
          :key="placement.node_id || index"
          class="text-body-2 mb-1"
        >
          <span v-if="placement.node_id">#{{ placement.node_id }} · </span>
          <strong>{{ placement.selected_runner_name || $t('noEligibleRunner') }}</strong>
          <span v-if="placement.selected_scope"> · {{ placement.selected_scope }}</span>
          <span v-if="placement.requested_executor_image">
            · {{ placement.requested_executor_image }}
          </span>
          <v-chip
            v-if="placement.provisional"
            x-small
            outlined
            class="ml-1"
            data-testid="placement-provisional"
          >{{ $t('provisional') }}</v-chip>
        </div>
      </div>

      <div v-if="plan.commands && plan.commands.length">
        <div class="text-caption text--secondary mb-1">{{ $t('commandShape') }}</div>
        <div
          v-for="(command, index) in plan.commands"
          :key="command.node_id || index"
          class="text-body-2 mb-1"
        >
          <span v-if="command.node_id">#{{ command.node_id }} · </span>
          <code>{{ command.application || 'default' }}</code>
          <span v-if="command.playbook"> · {{ command.playbook }}</span>
          <span v-if="command.argument_keys && command.argument_keys.length">
            · {{ command.argument_keys.join(', ') }}
          </span>
        </div>
      </div>

    </v-card-text>
  </v-card>
</template>

<script>
export default {
  props: {
    plan: { type: Object, default: null },
  },
  computed: {
    visibleFindings() {
      // Provisional placement is normal preview state, shown beside the runner.
      // A denial must remain visible even if a server reuses this reason code.
      return (this.plan?.findings || []).filter((finding) => (
        finding.code !== 'provisional_placement'
        || !['info', 'warning'].includes(finding.severity)
      ));
    },
  },
  methods: {
    findingType(severity) {
      if (severity === 'denial') return 'error';
      if (severity === 'warning') return 'warning';
      return 'info';
    },
    policyFindingLabel(finding) {
      if (!finding?.policy_scope || !finding?.policy_rule_id || !finding?.policy_revision) {
        return '';
      }
      return `${finding.policy_scope} · ${finding.policy_rule_id} · revision ${finding.policy_revision}`;
    },
    safeRemediationURL(value) {
      if (!value) return null;
      try {
        const parsed = new URL(value);
        if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.hash) {
          return null;
        }
        return parsed.href;
      } catch (error) {
        return null;
      }
    },
    referenceKey(reference) {
      return `${reference.kind}-${reference.id}-${reference.binding_target || ''}`;
    },
  },
};
</script>

<style scoped>
.execution-review {
  overflow-wrap: anywhere;
}

.execution-references {
  width: 100%;
  table-layout: fixed;
  border-collapse: collapse;
  text-align: left;
}

.execution-references th,
.execution-references td {
  padding: 6px 8px;
  vertical-align: top;
  border-bottom: 1px solid rgba(128, 128, 128, 0.2);
}

.execution-references th:first-child,
.execution-references td:first-child {
  padding-left: 0;
  width: 45%;
}

.execution-references th:last-child,
.execution-references td:last-child {
  padding-right: 0;
}

.execution-review .v-chip {
  max-width: 100%;
  height: auto;
  min-height: 24px;
}

.execution-review ::v-deep .v-chip__content {
  white-space: normal;
  height: auto;
}
</style>
