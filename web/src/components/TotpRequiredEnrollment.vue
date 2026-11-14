<template>
  <div data-testid="auth-totp-enrollment">
    <v-alert v-if="error" color="error" class="mb-5">
      {{ error }}
    </v-alert>
    <v-alert type="warning" density="compact" variant="outlined">
      Your administrator requires TOTP before this account can continue.
    </v-alert>

    <template v-if="!ceremony">
      <v-text-field
        v-model="reauthentication"
        data-testid="auth-totp-reauthentication"
        :label="$t('totpConfirmPassword')"
        type="password"
        autocomplete="current-password"
        variant="outlined"
        density="compact"
      />
      <v-btn
        data-testid="auth-totp-begin"
        block
        color="primary"
        :loading="saving"
        :disabled="!reauthentication"
        @click="beginEnrollment"
      >
        Generate enrollment codes
      </v-btn>
    </template>

    <template v-else>
      <p>{{ $t('totpScanQr') }}</p>
      <img
        data-testid="auth-totp-qr"
        :src="qrUrl"
        class="totp-required-enrollment__qr"
        alt="TOTP enrollment QR code"
      />
      <div class="mt-3" data-testid="auth-totp-secret">
        <strong>{{ $t('totpSecret') }}</strong>
        <code>{{ groupedSecret }}</code>
        <v-btn size="small" variant="text" @click="copyToClipboard(secret)">{{ $t('copy') }}</v-btn>
      </div>
      <div class="mt-2" data-testid="auth-totp-uri">
        <strong>{{ $t('totpProvisioningUri') }}</strong>
        <v-btn
          size="small"
          variant="text"
          @click="copyToClipboard(ceremony.provisioning_uri)"
        >{{ $t('copy') }}</v-btn>
      </div>

      <div class="subtitle-1 mt-5 mb-2">{{ $t('totpRecoveryCodesTitle') }}</div>
      <p>{{ $t('totpStoreCodes') }}</p>
      <div
        class="totp-required-enrollment__recovery-grid"
        data-testid="auth-totp-recovery-codes"
      >
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

      <template v-if="stage === 'confirm'">
        <v-text-field
          v-model="passcode"
          data-testid="auth-totp-confirm-code"
          class="mt-5"
          :label="$t('totpSixDigitCode')"
          inputmode="numeric"
          maxlength="6"
          variant="outlined"
          density="compact"
          :error-messages="passcodeError"
        />
        <v-btn
          data-testid="auth-totp-confirm"
          block
          color="primary"
          :loading="saving"
          :disabled="passcode.length !== 6"
          @click="confirmEnrollment"
        >
          {{ $t('totpVerifyCode') }}
        </v-btn>
      </template>

      <template v-else>
        <v-checkbox
          v-model="recoveryStored"
          data-testid="auth-totp-recovery-ack-checkbox"
          :label="$t('totpStoredCodes')"
        />
        <v-btn
          data-testid="auth-totp-recovery-ack"
          block
          color="primary"
          :loading="saving"
          :disabled="!recoveryStored"
          @click="acknowledgeRecovery"
        >
          Activate and continue
        </v-btn>
      </template>
    </template>

    <div class="text-center pt-6">
      <a @click="$emit('cancel')">{{ $t('Return to login') }}</a>
    </div>
  </div>
</template>

<script>
import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export default {
  emits: ['cancel', 'complete'],
  data() {
    return {
      ceremony: null,
      reauthentication: '',
      passcode: '',
      recoveryStored: false,
      stage: 'confirm',
      saving: false,
      error: null,
      passcodeError: '',
    };
  },
  computed: {
    qrUrl() {
      if (!this.ceremony) return null;
      return `${document.baseURI}api/auth/totp/enroll/${this.ceremony.id}/qr`;
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
  methods: {
    async beginEnrollment() {
      await this.run(async () => {
        this.ceremony = (await axios.post('/api/auth/totp/enroll', {
          reauthentication: this.reauthentication,
        })).data;
        this.stage = 'confirm';
      });
    },
    async confirmEnrollment() {
      this.passcodeError = '';
      await this.run(async () => {
        await axios.post(`/api/auth/totp/enroll/${this.ceremony.id}/confirm`, {
          reauthentication: this.reauthentication,
          passcode: this.passcode,
        });
        this.stage = 'acknowledge';
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
    async acknowledgeRecovery() {
      await this.run(async () => {
        await axios.post(
          `/api/auth/totp/enroll/${this.ceremony.id}/recovery-codes/acknowledge`,
          { stored: this.recoveryStored },
        );
        this.$emit('complete');
      });
    },
    async run(operation) {
      this.saving = true;
      this.error = null;
      try {
        await operation();
      } catch (error) {
        this.error = getErrorMessage(error);
        if (this.stage === 'confirm') {
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
.totp-required-enrollment__qr {
  width: min(100%, 280px);
  display: block;
  margin: 0 auto;
  border: 10px solid white;
  border-radius: 4px;
  background: white;
}

.totp-required-enrollment__recovery-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.totp-required-enrollment__recovery-grid code {
  padding: 7px;
  text-align: center;
  user-select: all;
}
</style>
