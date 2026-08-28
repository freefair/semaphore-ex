<template>
  <div data-testid="auth-totp-enrollment">
    <v-alert v-if="error" color="error" class="mb-5">
      {{ error }}
    </v-alert>
    <v-alert type="warning" dense outlined>
      Your administrator requires TOTP before this account can continue.
    </v-alert>

    <template v-if="!ceremony">
      <v-text-field
        v-model="reauthentication"
        data-testid="auth-totp-reauthentication"
        label="Confirm your password"
        type="password"
        autocomplete="current-password"
        outlined
        dense
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
      <p>Scan this QR code with an authenticator app.</p>
      <img
        data-testid="auth-totp-qr"
        :src="qrUrl"
        class="totp-required-enrollment__qr"
        alt="TOTP enrollment QR code"
      />

      <div class="subtitle-1 mt-5 mb-2">Single-use recovery codes</div>
      <p>Store all codes. They cannot be displayed again.</p>
      <div
        class="totp-required-enrollment__recovery-grid"
        data-testid="auth-totp-recovery-codes"
      >
        <code v-for="code in ceremony.recovery_codes" :key="code">{{ code }}</code>
      </div>

      <template v-if="stage === 'confirm'">
        <v-text-field
          v-model="passcode"
          data-testid="auth-totp-confirm-code"
          class="mt-5"
          label="Six-digit code"
          inputmode="numeric"
          maxlength="6"
          outlined
          dense
        />
        <v-btn
          data-testid="auth-totp-confirm"
          block
          color="primary"
          :loading="saving"
          :disabled="passcode.length !== 6"
          @click="confirmEnrollment"
        >
          Verify code
        </v-btn>
      </template>

      <template v-else>
        <v-checkbox
          v-model="recoveryStored"
          data-testid="auth-totp-recovery-ack-checkbox"
          label="I stored these recovery codes"
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
  data() {
    return {
      ceremony: null,
      reauthentication: '',
      passcode: '',
      recoveryStored: false,
      stage: 'confirm',
      saving: false,
      error: null,
    };
  },
  computed: {
    qrUrl() {
      if (!this.ceremony) return null;
      return `${document.baseURI}api/auth/totp/enroll/${this.ceremony.id}/qr`;
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
      await this.run(async () => {
        await axios.post(`/api/auth/totp/enroll/${this.ceremony.id}/confirm`, {
          reauthentication: this.reauthentication,
          passcode: this.passcode,
        });
        this.stage = 'acknowledge';
      });
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
