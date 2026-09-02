<template>
  <v-card v-if="plan" outlined class="mt-4" data-testid="execution-preflight-review">
    <v-card-title class="text-subtitle-1 py-3">
      <v-icon small class="mr-2">mdi-clipboard-check-outline</v-icon>
      {{ $t('executionPreflightReview') }}
    </v-card-title>
    <v-card-text class="pt-0">
      <v-alert
        v-for="(finding, index) in plan.findings || []"
        :key="`${finding.code}-${finding.node_id || 0}-${index}`"
        :type="findingType(finding.severity)"
        dense
        text
        class="mb-2"
        :data-testid="`execution-preflight-${finding.severity}`"
      >
        {{ finding.message }} <code>{{ finding.code }}</code>
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
        <div
          v-for="reference in plan.references"
          :key="referenceKey(reference)"
          class="text-body-2"
        >
          <v-icon x-small class="mr-1">
            {{ reference.visible ? 'mdi-link-variant' : 'mdi-eye-off-outline' }}
          </v-icon>
          {{ reference.kind }} · {{ reference.name || `#${reference.id}` }}
          <span v-if="reference.binding_target">→ {{ reference.binding_target }}</span>
        </div>
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
          <v-chip x-small outlined class="ml-1">{{ $t('provisional') }}</v-chip>
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

      <div class="text-caption text--secondary mt-3">
        {{ $t('executionPreflightNoSecretValues') }}
      </div>
    </v-card-text>
  </v-card>
</template>

<script>
export default {
  props: {
    plan: { type: Object, default: null },
  },
  methods: {
    findingType(severity) {
      if (severity === 'denial') return 'error';
      if (severity === 'warning') return 'warning';
      return 'info';
    },
    referenceKey(reference) {
      return `${reference.kind}-${reference.id}-${reference.binding_target || ''}`;
    },
  },
};
</script>
