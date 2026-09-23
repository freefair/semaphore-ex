import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

const PREFLIGHT_FINGERPRINT_HEADER = 'X-Semaphore-Preflight-Fingerprint';
const PREFLIGHT_REVIEW_HEADER = ['X-Semaphore-Preflight', 'Token'].join('-');

export const enhancedComputed = {
  executionPreflightSignature() {
    return this.executionPreflightInitialized && this.item
      ? JSON.stringify(this.taskSavePayload()) : null;
  },
  executionReady() {
    return Boolean(this.executionPreflightSignature
      && this.executionPreflightSignature === this.executionPreflightPayloadSignature
      && !this.executionPreflightLoading && !this.executionPreflightError && !this.formSaving
      && (this.executionPreflightUnavailable || this.executionPreflight)
      && !(this.executionPreflight?.findings || []).some(({ severity }) => severity === 'denial'));
  },
  deploymentWindowOverrideCategories() {
    return [
      { text: this.$t('deploymentWindowOverrideIncident'), value: 'incident' },
      { text: this.$t('deploymentWindowOverrideSecurity'), value: 'security' },
      { text: this.$t('deploymentWindowOverrideCustomerImpact'), value: 'customer_impact' },
    ];
  },
  deploymentWindowOverrideReady() {
    return Boolean(this.deploymentWindowBlock
        && this.deploymentWindowOverrideConfirmed
        && ['incident', 'security', 'customer_impact']
          .includes(this.deploymentWindowOverrideCategory)
        && /^[A-Z0-9]{2,16}-[1-9][0-9]{0,9}$/
          .test(this.deploymentWindowOverrideReference));
  },
  deploymentWindowBlockMessage() {
    return this.$t(`deploymentWindowBlocked_${this.deploymentWindowBlock?.reason || 'unknown'}`);
  },
};

export const enhancedWatch = {
  executionPreflightSignature: {
    immediate: true,
    handler: 'scheduleExecutionPreflight',
  },
  executionReady: {
    immediate: true,
    handler(ready) { this.$emit('execution-ready', ready); },
  },
};

export const enhancedMethods = {
  scheduleExecutionPreflight() {
    clearTimeout(this.executionPreflightTimer);
    this.executionPreflightRequestId += 1;
    this.executionPreflight = null;
    this.executionPreflightPayloadSignature = null;
    this.executionPreflightError = null;
    this.executionPreflightUnavailable = false;
    if (this.formError === this.$t('executionPreflightChanged')) this.formError = null;
    this.executionPreflightLoading = Boolean(this.executionPreflightSignature);
    if (this.executionPreflightSignature) {
      this.executionPreflightTimer = setTimeout(() => this.refreshExecutionPreflight(), 250);
    }
  },
  async refreshExecutionPreflight() {
    clearTimeout(this.executionPreflightTimer);
    const signature = this.executionPreflightSignature;
    if (!signature) return;
    const requestId = this.executionPreflightRequestId + 1;
    this.executionPreflightRequestId = requestId;
    this.executionPreflightLoading = true;
    this.executionPreflightError = null;
    const isCurrent = () => requestId === this.executionPreflightRequestId
      && signature === this.executionPreflightSignature;
    try {
      const { data, headers } = await axios.post(`/api/project/${this.projectId}/tasks/preflight`, JSON.parse(signature));
      if (!isCurrent()) return;
      if (this.executionPreflight && this.executionPreflight.fingerprint !== data.fingerprint) {
        this.formError = this.$t('executionPreflightChanged');
      }
      this.executionPreflight = data;
      this.executionPreflightPayloadSignature = signature;
      this.scheduleExecutionPreflightExpiry(data, headers?.date);
    } catch (err) {
      if (!isCurrent()) return;
      if (this.isExecutionPreflightUnavailable(err)) {
        this.executionPreflightUnavailable = true;
        this.executionPreflightPayloadSignature = signature;
      } else {
        this.executionPreflightError = getErrorMessage(err);
      }
    } finally {
      if (isCurrent()) this.executionPreflightLoading = false;
    }
  },
  scheduleExecutionPreflightExpiry(review, serverDate) {
    clearTimeout(this.executionPreflightTimer);
    // Both timestamps come from the server; a skewed browser clock must not
    // turn review refresh into a tight polling loop.
    const refreshDelay = Date.parse(review.expires_at) - Date.parse(serverDate) - 5000;
    if (Number.isFinite(refreshDelay) && refreshDelay > 0) {
      this.executionPreflightTimer = setTimeout(() => {
        this.refreshExecutionPreflight();
      }, refreshDelay);
    }
  },
  disposeExecutionPreflight() {
    clearTimeout(this.executionPreflightTimer);
    this.executionPreflightRequestId += 1;
  },
  async save() {
    if (!this.executionReady) {
      this.$emit('error', {});
      return null;
    }
    this.formError = null;
    if (!this.$refs.form.validate()) {
      this.$emit('error', {});
      return null;
    }
    if (this.deploymentWindowBlock && !this.deploymentWindowOverrideReady) {
      this.formError = this.$t('deploymentWindowOverrideIncomplete');
      this.$emit('error', { message: this.formError });
      return null;
    }
    this.formSaving = true;
    const signature = this.executionPreflightSignature;
    try {
      const payload = JSON.parse(signature);
      if (signature !== this.executionPreflightPayloadSignature) {
        this.scheduleExecutionPreflight();
        this.$emit('error', {});
        return null;
      }
      if (this.executionPreflightUnavailable) {
        return await this.submitTaskPayload(this.taskStartPayload(payload));
      }
      if ((this.executionPreflight.findings || []).some(({ severity }) => severity === 'denial')) {
        this.formError = this.$t('executionPreflightDenied');
        this.$emit('preflight', this.executionPreflight);
        return null;
      }
      return await this.submitTaskPayload(this.taskStartPayload(payload), {
        [PREFLIGHT_FINGERPRINT_HEADER]: this.executionPreflight.fingerprint,
        [PREFLIGHT_REVIEW_HEADER]: this.executionPreflight.review_token,
      });
    } catch (err) {
      if (this.isDeploymentWindowBlock(err)) {
        this.adoptDeploymentWindowBlock(err.response.data);
        this.$emit('error', {});
        return null;
      }
      if (err?.response?.status === 403
        && err?.response?.data?.error === 'DEPLOYMENT_WINDOW_OVERRIDE_FORBIDDEN') {
        this.formError = this.$t('deploymentWindowOverrideForbidden');
        this.$emit('error', { message: this.formError });
        return null;
      }
      const fresh = err?.response?.data?.preflight;
      if (err?.response?.status === 409 && fresh) {
        if (signature === this.executionPreflightSignature) {
          this.executionPreflight = fresh;
          this.executionPreflightPayloadSignature = signature;
          this.formError = this.$t('executionPreflightChanged');
          this.scheduleExecutionPreflightExpiry(fresh, err.response.headers?.date);
        } else {
          this.scheduleExecutionPreflight();
        }
        this.$emit('preflight', fresh);
        return null;
      }
      this.formError = getErrorMessage(err);
      this.$emit('error', { message: this.formError });
      return null;
    } finally {
      this.formSaving = false;
    }
  },
  taskSavePayload() {
    return {
      ...this.item,
      project_id: this.projectId,
      environment: JSON.stringify(this.editedEnvironment),
      secret: JSON.stringify(this.editedSecretEnvironment),
    };
  },
  taskStartPayload(payload) {
    if (!this.deploymentWindowOverrideReady) return payload;
    return {
      ...payload,
      deployment_window_override: {
        category: this.deploymentWindowOverrideCategory,
        reference: this.deploymentWindowOverrideReference,
      },
    };
  },
  isDeploymentWindowBlock(err) {
    return err?.response?.status === 409
        && err?.response?.data?.state === 'blocked';
  },
  adoptDeploymentWindowBlock(decision) {
    this.deploymentWindowBlock = decision;
    this.deploymentWindowOverrideCategory = null;
    this.deploymentWindowOverrideReference = '';
    this.deploymentWindowOverrideConfirmed = false;
    this.formError = null;
  },
  clearDeploymentWindowBlock() {
    this.deploymentWindowBlock = null;
    this.deploymentWindowOverrideCategory = null;
    this.deploymentWindowOverrideReference = '';
    this.deploymentWindowOverrideConfirmed = false;
  },
  formatDeploymentWindowDate(value) {
    return new Date(value).toLocaleString();
  },
  isExecutionPreflightUnavailable(err) {
    const response = err?.response;
    return response?.status === 404
        && response?.data?.error === 'CAPABILITY_DENIED'
        && response?.data?.capability === 'execution_preflight';
  },
  async submitTaskPayload(payload, headers = {}) {
    const item = (await axios({
      method: 'post',
      url: this.getItemsUrl(),
      responseType: 'json',
      data: payload,
      headers,
    })).data;
    await this.afterSave(item);
    this.$emit('save', { item: item || this.item, action: this.getSaveAction() });
    return item || this.item;
  },
};
