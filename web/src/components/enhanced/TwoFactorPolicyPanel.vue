<template>
  <section data-testid="totp-rollout" class="two-factor-policy">
    <h2 class="text-title-large font-weight-medium mb-1">{{ $t('twoFactorTitle') }}</h2>
    <p class="mb-4">{{ $t('twoFactorIntro') }}</p>

    <v-radio-group
      v-model="totpRollout.state"
      data-testid="totp-rollout-state"
      hide-details
      class="mb-2"
    >
      <v-radio
        v-for="option in states"
        :key="option.value"
        :value="option.value"
        :data-testid="`totp-rollout-option-${option.value}`"
      >
        <template v-slot:label>
          <div>
            <div class="font-weight-medium">{{ $t(option.label) }}</div>
            <div class="text-body-small text-medium-emphasis">{{ $t(option.description) }}</div>
          </div>
        </template>
      </v-radio>
    </v-radio-group>

    <v-autocomplete
      v-if="totpRollout.state === 'required_selected'"
      v-model="totpRollout.selected_user_ids"
      data-testid="totp-selected-users"
      :items="totpUsers"
      item-title="username"
      item-value="id"
      :label="$t('twoFactorSelectedUsers')"
      multiple
      chips
      closable-chips
      variant="outlined"
      density="compact"
      class="mt-2"
    />

    <div class="two-factor-policy__actions">
      <v-btn
        data-testid="totp-rollout-save"
        color="primary"
        :loading="totpRolloutSaving"
        :disabled="!changed"
        @click="saveTotpRollout"
      >
        {{ $t('twoFactorApply') }}
      </v-btn>
      <span
        v-if="saved"
        class="text-success"
        role="status"
        data-testid="totp-rollout-saved"
      >{{ $t('twoFactorSaved', { state: stateLabel(appliedState) }) }}</span>
    </div>

    <v-alert
      v-if="totpRolloutError"
      type="error"
      variant="outlined"
      density="compact"
      class="mt-3"
      role="alert"
      data-testid="totp-rollout-error"
    >
      <div>{{ errorText(totpRolloutError) }}</div>
      <div v-if="errorAction(totpRolloutError)" class="mt-1">
        <a href="#" @click.prevent="openAccount">{{ $t(errorAction(totpRolloutError)) }}</a>
      </div>
    </v-alert>

    <template v-if="totpTransitions.length">
      <h3 class="text-title-small font-weight-medium mt-6 mb-2">{{ $t('twoFactorHistory') }}</h3>
      <v-table density="compact" data-testid="totp-rollout-history">
        <thead>
          <tr>
            <th>{{ $t('twoFactorHistoryChange') }}</th>
            <th>{{ $t('twoFactorHistoryBy') }}</th>
            <th>{{ $t('twoFactorHistoryWhen') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="transition in totpTransitions.slice(0, 8)" :key="transition.id">
            <td>{{ stateLabel(transition.from_state) }} → {{ stateLabel(transition.to_state) }}</td>
            <td>{{ actorName(transition.actor_id) }}</td>
            <td>{{ new Date(transition.created).toLocaleString() }}</td>
          </tr>
        </tbody>
      </v-table>
    </template>
  </section>
</template>

<script>
import axios from 'axios';
import EventBus from '@/event-bus';

export const TWO_FACTOR_STATES = [
  { value: 'disabled', label: 'twoFactorStateDisabled', description: 'twoFactorStateDisabledHint' },
  { value: 'shadow', label: 'twoFactorStateShadow', description: 'twoFactorStateShadowHint' },
  { value: 'optional', label: 'twoFactorStateOptional', description: 'twoFactorStateOptionalHint' },
  {
    value: 'required_selected',
    label: 'twoFactorStateRequiredSelected',
    description: 'twoFactorStateRequiredSelectedHint',
  },
  { value: 'required', label: 'twoFactorStateRequired', description: 'twoFactorStateRequiredHint' },
];

const ERROR_TEXT = {
  TOTP_ADMIN_RECOVERY_NOT_READY: 'twoFactorErrorAdminRecovery',
};

const ERROR_ACTION = {
  TOTP_ADMIN_RECOVERY_NOT_READY: 'twoFactorErrorAdminRecoveryAction',
};

export default {
  data() {
    return {
      states: TWO_FACTOR_STATES,
      totpRollout: { state: 'disabled', selected_user_ids: [] },
      appliedState: null,
      appliedUsers: [],
      users: [],
      totpTransitions: [],
      totpRolloutSaving: false,
      totpRolloutError: null,
      saved: false,
    };
  },

  computed: {
    totpUsers() {
      return this.users.filter((user) => !user.external);
    },

    changed() {
      if (this.totpRollout.state !== this.appliedState) return true;
      const selected = [...(this.totpRollout.selected_user_ids || [])].sort();
      const applied = [...this.appliedUsers].sort();
      return JSON.stringify(selected) !== JSON.stringify(applied);
    },
  },

  watch: {
    'totpRollout.state': function stateChanged() {
      this.saved = false;
    },
  },

  created() {
    this.loadTotpRollout();
  },

  methods: {
    async loadTotpRollout() {
      this.totpRolloutError = null;
      try {
        const [configuration, users, transitions] = await Promise.all([
          axios.get('/api/capabilities/totp'),
          axios.get('/api/users'),
          axios.get('/api/capabilities/totp/transitions'),
        ]);
        this.totpRollout = {
          state: configuration.data.state,
          selected_user_ids: configuration.data.selected_user_ids || [],
        };
        this.appliedState = this.totpRollout.state;
        this.appliedUsers = [...this.totpRollout.selected_user_ids];
        this.users = users.data || [];
        this.totpTransitions = transitions.data || [];
      } catch (error) {
        this.totpRolloutError = error.response?.data?.error || error.message;
      }
    },

    async saveTotpRollout() {
      this.totpRolloutSaving = true;
      this.totpRolloutError = null;
      this.saved = false;
      try {
        await axios.put('/api/capabilities/totp', this.totpRollout);
        await this.loadTotpRollout();
        this.saved = true;
        EventBus.$emit('i-account-change');
      } catch (error) {
        this.totpRolloutError = error.response?.data?.error || error.message;
      } finally {
        this.totpRolloutSaving = false;
      }
    },

    stateLabel(value) {
      const state = TWO_FACTOR_STATES.find((option) => option.value === value);
      return state ? this.$t(state.label) : value;
    },

    actorName(actorId) {
      const user = this.users.find((candidate) => candidate.id === actorId);
      return user ? user.name || user.username : `#${actorId}`;
    },

    errorText(code) {
      return ERROR_TEXT[code] ? this.$t(ERROR_TEXT[code]) : code;
    },

    errorAction(code) {
      return ERROR_ACTION[code] || null;
    },

    openAccount() {
      EventBus.$emit('i-edit-account');
    },
  },
};
</script>

<style scoped>
.two-factor-policy__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  margin-top: 12px;
}
</style>
