<template>
  <section data-testid="totp-enrollment-panel">
    <div class="totp-heading mb-3">
      <div class="title">{{ $t('twoFactorTitle') }}</div>
      <v-chip
        v-if="status"
        data-testid="totp-capability-state"
        :color="capabilityStateColor(status.capability_state)"
        size="small"
        dark
      >
        {{ status.capability_state.replace('_', ' ') }}
      </v-chip>
    </div>

    <v-progress-linear v-if="loading" indeterminate color="primary" />
    <v-alert
      v-if="error"
      data-testid="totp-error"
      type="error"
      density="compact"
      variant="outlined"
    >
      {{ error }}
    </v-alert>

    <template v-if="status && !loading">
      <v-alert
        v-if="status.required"
        data-testid="totp-required"
        type="warning"
        density="compact"
        variant="outlined"
      >
        TOTP enrollment is required by the current rollout policy.
      </v-alert>
      <v-alert
        v-if="status.capability_state === 'disabled' || status.capability_state === 'shadow'"
        type="info"
        density="compact"
        variant="outlined"
      >
        <span v-if="status.capability_state === 'shadow'">
          TOTP is in shadow mode. Enforcement and enrollment are not yet user-visible.
        </span>
        <span v-else>TOTP enrollment is disabled by an administrator.</span>
      </v-alert>

      <template v-else-if="status.enrollment_state === 'active'">
        <v-alert data-testid="totp-active" type="success" density="compact" variant="outlined">
          TOTP is active. {{ status.recovery_codes_remaining }} unused recovery codes remain.
        </v-alert>
        <v-text-field
          v-if="isSelf"
          v-model="reauthentication"
          :label="$t('totpCurrentPassword')"
          type="password"
          autocomplete="current-password"
          variant="outlined"
          density="compact"
        />
        <v-btn
          data-testid="totp-reset"
          color="error"
          variant="outlined"
          :loading="saving"
          :disabled="isSelf && !reauthentication"
          @click="resetEnrollment"
        >
          {{ $t('totpReset') }}
        </v-btn>
      </template>

      <template v-else-if="isSelf">
        <v-text-field
          v-model="reauthentication"
          :label="$t('totpCurrentPassword')"
          type="password"
          autocomplete="current-password"
          variant="outlined"
          density="compact"
        />

        <v-btn
          v-if="!ceremony"
          data-testid="totp-begin"
          color="primary"
          :loading="saving"
          :disabled="!reauthentication"
          @click="requestEnrollment"
        >
          {{ status.enrollment_state === 'none' ? $t('totpSetUp') : $t('totpRestart') }}
        </v-btn>

        <v-alert
          v-if="restartConfirmation"
          type="warning"
          variant="outlined"
          density="compact"
          class="mt-3"
          role="alert"
          data-testid="totp-restart-confirmation"
        >
          <div>{{ $t('totpRestartWarning') }}</div>
          <div class="d-flex flex-wrap ga-2 mt-2">
            <v-btn
              size="small"
              color="warning"
              data-testid="totp-restart-confirm"
              @click="beginEnrollment"
            >{{ $t('totpRestart') }}</v-btn>
            <v-btn size="small" variant="text" @click="restartConfirmation = false">
              {{ $t('cancel') }}
            </v-btn>
          </div>
        </v-alert>

        <v-card
          v-if="ceremony"
          data-testid="totp-ceremony"
          variant="outlined"
          class="mt-3"
          style="background: var(--highlighted-card-bg-color)"
        >
          <v-card-text>
            <p>{{ $t('totpScanQr') }}</p>
            <img
              data-testid="totp-qr"
              :src="qrUrl"
              class="totp-qr"
              alt="TOTP enrollment QR code"
            />
            <div class="mt-3" data-testid="totp-secret">
              <strong>{{ $t('totpSecret') }}</strong>
              <code>{{ groupedSecret }}</code>
              <v-btn
                size="small"
                variant="text"
                @click="copyToClipboard(secret)"
              >{{ $t('copy') }}</v-btn>
            </div>
            <div class="mt-2" data-testid="totp-uri">
              <strong>{{ $t('totpProvisioningUri') }}</strong>
              <v-btn
                size="small"
                variant="text"
                @click="copyToClipboard(ceremony.provisioning_uri)"
              >{{ $t('copy') }}</v-btn>
            </div>

            <div class="subtitle-1 mt-5 mb-2">{{ $t('totpRecoveryCodesTitle') }}</div>
            <p>{{ $t('totpStoreCodes') }}</p>
            <div class="recovery-grid" data-testid="totp-recovery-codes">
              <code v-for="code in ceremony.recovery_codes" :key="code">{{ code }}</code>
            </div>
            <v-btn
              size="small"
              variant="text"
              @click="copyToClipboard(recoveryCodesText)"
            >{{ $t('totpCopyAllRecoveryCodes') }}</v-btn>
            <v-btn
              size="small"
              variant="text"
              @click="downloadRecoveryCodes"
            >{{ $t('totpDownloadRecoveryCodes') }}</v-btn>

            <template v-if="status.enrollment_state === 'pending_confirmation'">
              <v-text-field
                v-model="passcode"
                data-testid="totp-confirm-code"
                :label="$t('totpSixDigitCode')"
                inputmode="numeric"
                maxlength="6"
                variant="outlined"
                density="compact"
                class="mt-5"
                :error-messages="passcodeError"
              />
              <v-btn
                data-testid="totp-confirm"
                color="primary"
                :loading="saving"
                :disabled="passcode.length !== 6 || !reauthentication"
                @click="confirmEnrollment"
              >
                {{ $t('totpVerifyCode') }}
              </v-btn>
            </template>

            <template v-else-if="status.enrollment_state === 'pending_recovery_ack'">
              <v-checkbox
                v-model="recoveryStored"
                data-testid="totp-recovery-ack-checkbox"
                :label="$t('totpStoredCodes')"
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
          density="compact"
          variant="outlined"
          class="mt-3"
        >
          Recovery codes cannot be displayed again. Restart enrollment to generate a new set.
        </v-alert>
      </template>

      <v-alert v-else type="info" density="compact" variant="outlined">
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
      restartConfirmation: false,
      status: null,
      ceremony: null,
      reauthentication: '',
      passcode: '',
      recoveryStored: false,
      loading: true,
      saving: false,
      error: null,
      passcodeError: '',
    };
  },

  computed: {
    qrUrl() {
      if (!this.ceremony) return null;
      return `${document.baseURI}api/users/${this.itemId}/2fas/totp/${this.ceremony.id}/qr`;
    },
    secret() {
      return this.ceremony ? new URL(this.ceremony.provisioning_uri).searchParams.get('secret') || '' : '';
    },
    groupedSecret() {
      return this.secret.match(/.{1,4}/g)?.join(' ') || '';
    },
    recoveryCodesText() {
      return this.ceremony.recovery_codes.join('\n');
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

    requestEnrollment() {
      if (this.status.enrollment_state === 'none') {
        this.beginEnrollment();
      } else {
        this.restartConfirmation = true;
      }
    },

    async beginEnrollment() {
      this.restartConfirmation = false;
      await this.run(async () => {
        this.ceremony = (await axios.post(`/api/users/${this.itemId}/2fas/totp`, {
          reauthentication: this.reauthentication,
        })).data;
        await this.loadStatus();
      });
    },

    async confirmEnrollment() {
      this.passcodeError = '';
      await this.run(async () => {
        this.status = (await axios.post(
          `/api/users/${this.itemId}/2fas/totp/${this.ceremony.id}/confirm`,
          { reauthentication: this.reauthentication, passcode: this.passcode },
        )).data;
      });
    },
    async copyToClipboard(value) {
      await navigator.clipboard.writeText(value);
    },
    downloadRecoveryCodes() {
      const link = document.createElement('a');
      const url = URL.createObjectURL(new Blob([`${this.recoveryCodesText}\n`], { type: 'text/plain' }));
      link.href = url;
      link.download = 'semaphore-recovery-codes.txt';
      link.click();
      URL.revokeObjectURL(url);
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
        if (this.status && this.status.enrollment_state === 'pending_confirmation') {
          this.passcodeError = this.error === 'INVALID_PASSCODE'
            ? this.$t('totpInvalidPasscode')
            : this.error;
        }
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
