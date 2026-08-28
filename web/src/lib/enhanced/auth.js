import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

export const enhancedComputed = {
  requiredEnrollmentQrUrl() {
    if (!this.enrollmentCeremony) return null;
    return `${document.baseURI}api/auth/totp/enroll/${this.enrollmentCeremony.id}/qr`;
  },
};

export const enhancedMethods = {
  async beginRequiredEnrollment() {
    await this.runEnrollment(async () => {
      this.enrollmentCeremony = (await axios.post('/api/auth/totp/enroll', {
        reauthentication: this.enrollmentReauthentication,
      })).data;
      this.enrollmentStage = 'confirm';
    });
  },
  async confirmRequiredEnrollment() {
    await this.runEnrollment(async () => {
      await axios.post(
        `/api/auth/totp/enroll/${this.enrollmentCeremony.id}/confirm`,
        {
          reauthentication: this.enrollmentReauthentication,
          passcode: this.enrollmentCode,
        },
      );
      this.enrollmentStage = 'acknowledge';
    });
  },
  async acknowledgeRequiredRecovery() {
    await this.runEnrollment(async () => {
      await axios.post(
        `/api/auth/totp/enroll/${this.enrollmentCeremony.id}/recovery-codes/acknowledge`,
        { stored: this.enrollmentRecoveryStored },
      );
      this.redirectAfterLogin();
    });
  },
  async runEnrollment(operation) {
    this.signInProcess = true;
    this.signInError = null;
    try {
      await operation();
    } catch (error) {
      this.signInError = getErrorMessage(error);
    } finally {
      this.signInProcess = false;
    }
  },
};
