<template>
  <section data-testid="totp-enrollment-panel">
    <div class="totp-heading mb-3">
      <div class="title">Two-factor authentication</div>
      <v-chip
        v-if="status"
        data-testid="totp-capability-state"
        :color="capabilityStateColor(status.capability_state)"
        small
        dark
      >
        {{ status.capability_state.replace('_', ' ') }}
      </v-chip>
    </div>

    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <v-alert v-if="error" data-testid="totp-error" type="error" dense outlined>
      {{ error }}
    </v-alert>

    <template v-if="status && !loading">
      <v-alert v-if="status.required" data-testid="totp-required" type="warning" dense outlined>
        TOTP enrollment is required by the current rollout policy.
      </v-alert>
      <v-alert
        v-if="status.capability_state === 'disabled' || status.capability_state === 'shadow'"
        type="info"
        dense
        outlined
      >
        <span v-if="status.capability_state === 'shadow'">
          TOTP is in shadow mode. Enforcement and enrollment are not yet user-visible.
        </span>
        <span v-else>TOTP enrollment is disabled by an administrator.</span>
      </v-alert>

      <template v-else-if="status.enrollment_state === 'active'">
        <v-alert data-testid="totp-active" type="success" dense outlined>
          TOTP is active. {{ status.recovery_codes_remaining }} unused recovery codes remain.
        </v-alert>
        <v-text-field
          v-if="isSelf"
          v-model="reauthentication"
          label="Current password"
          type="password"
          autocomplete="current-password"
          outlined
          dense
        />
        <v-btn
          data-testid="totp-reset"
          color="error"
          outlined
          :loading="saving"
          :disabled="isSelf && !reauthentication"
          @click="resetEnrollment"
        >
          Reset TOTP and revoke sessions
        </v-btn>
      </template>

      <template v-else-if="isSelf">
        <v-text-field
          v-model="reauthentication"
          label="Current password"
          type="password"
          autocomplete="current-password"
          outlined
          dense
        />

        <v-btn
          v-if="!ceremony"
          data-testid="totp-begin"
          color="primary"
          :loading="saving"
          :disabled="!reauthentication"
          @click="beginEnrollment"
        >
          {{ status.enrollment_state === 'none' ? 'Set up TOTP' : 'Restart enrollment' }}
        </v-btn>

        <v-card
          v-if="ceremony"
          data-testid="totp-ceremony"
          outlined
          class="mt-3"
          style="background: var(--highlighted-card-bg-color)"
        >
          <v-card-text>
            <p>Scan this QR code with an authenticator app.</p>
            <img
              data-testid="totp-qr"
              :src="qrUrl"
              class="totp-qr"
              alt="TOTP enrollment QR code"
            />

            <div class="subtitle-1 mt-5 mb-2">Single-use recovery codes</div>
            <p>Store every code before continuing. They will not be displayed again.</p>
            <div class="recovery-grid" data-testid="totp-recovery-codes">
              <code v-for="code in ceremony.recovery_codes" :key="code">{{ code }}</code>
            </div>

            <template v-if="status.enrollment_state === 'pending_confirmation'">
              <v-text-field
                v-model="passcode"
                data-testid="totp-confirm-code"
                label="Six-digit code"
                inputmode="numeric"
                maxlength="6"
                outlined
                dense
                class="mt-5"
              />
              <v-btn
                data-testid="totp-confirm"
                color="primary"
                :loading="saving"
                :disabled="passcode.length !== 6 || !reauthentication"
                @click="confirmEnrollment"
              >
                Verify code
              </v-btn>
            </template>

            <template v-else-if="status.enrollment_state === 'pending_recovery_ack'">
              <v-checkbox
                v-model="recoveryStored"
                data-testid="totp-recovery-ack-checkbox"
                label="I stored these recovery codes"
              />
              <v-btn
                data-testid="totp-recovery-ack"
                color="primary"
                :loading="saving"
                :disabled="!recoveryStored"
                @click="acknowledgeRecoveryCodes"
              >
                Activate TOTP
              </v-btn>
            </template>
          </v-card-text>
        </v-card>

        <v-alert
          v-else-if="status.enrollment_state === 'pending_recovery_ack'"
          type="warning"
          dense
          outlined
          class="mt-3"
        >
          Recovery codes cannot be displayed again. Restart enrollment to generate a new set.
        </v-alert>
      </template>

      <v-alert v-else type="info" dense outlined>
        This user must complete enrollment from their own authenticated session.
      </v-alert>
    </template>
  </section>
</template>

<script>
import axios from 'axios';
import { capabilityStateColor } from '@/lib/capabilities';
import { getErrorMessage } from '@/lib/error';

export default {
  props: {
    itemId: {
      type: [Number, String],
      required: true,
    },
    isSelf: Boolean,
  },

  data() {
    return {
      status: null,
      ceremony: null,
      reauthentication: '',
      passcode: '',
      recoveryStored: false,
      loading: true,
      saving: false,
      error: null,
    };
  },

  computed: {
    qrUrl() {
      if (!this.ceremony) return null;
      return `${document.baseURI}api/users/${this.itemId}/2fas/totp/${this.ceremony.id}/qr`;
    },
  },

  mounted() {
    this.loadStatus();
  },

  methods: {
    capabilityStateColor,

    async loadStatus() {
      this.loading = true;
      this.error = null;
      try {
        this.status = (await axios.get(`/api/users/${this.itemId}/2fas/totp`)).data;
      } catch (error) {
        this.error = getErrorMessage(error);
      } finally {
        this.loading = false;
      }
    },

    async beginEnrollment() {
      await this.run(async () => {
        this.ceremony = (await axios.post(`/api/users/${this.itemId}/2fas/totp`, {
          reauthentication: this.reauthentication,
        })).data;
        await this.loadStatus();
      });
    },

    async confirmEnrollment() {
      await this.run(async () => {
        this.status = (await axios.post(
          `/api/users/${this.itemId}/2fas/totp/${this.ceremony.id}/confirm`,
          { reauthentication: this.reauthentication, passcode: this.passcode },
        )).data;
      });
    },

    async acknowledgeRecoveryCodes() {
      await this.run(async () => {
        this.status = (await axios.post(
          `/api/users/${this.itemId}/2fas/totp/${this.ceremony.id}/recovery-codes/acknowledge`,
          { stored: this.recoveryStored },
        )).data;
        this.ceremony = null;
        this.reauthentication = '';
        this.passcode = '';
      });
    },

    async resetEnrollment() {
      await this.run(async () => {
        await axios.delete(`/api/users/${this.itemId}/2fas/totp/${this.status.enrollment_id}`, {
          data: { reauthentication: this.reauthentication },
        });
        if (this.isSelf) {
          window.location.reload();
          return;
        }
        this.ceremony = null;
        await this.loadStatus();
      });
    },

    async run(operation) {
      this.saving = true;
      this.error = null;
      try {
        await operation();
      } catch (error) {
        this.error = getErrorMessage(error);
      } finally {
        this.saving = false;
      }
    },
  },
};
</script>

<style scoped>
.totp-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px;
}

.totp-qr {
  width: min(100%, 280px);
  aspect-ratio: 1;
  display: block;
  border: 10px solid white;
  border-radius: 4px;
  background: white;
}

.recovery-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(130px, 1fr));
  gap: 8px;
}

.recovery-grid code {
  padding: 8px;
  text-align: center;
  user-select: all;
}
</style>
