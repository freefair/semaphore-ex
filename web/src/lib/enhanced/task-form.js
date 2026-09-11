import axios from 'axios';
import { getErrorMessage } from '@/lib/error';

const PREFLIGHT_FINGERPRINT_HEADER = 'X-Semaphore-Preflight-Fingerprint';
const PREFLIGHT_REVIEW_HEADER = ['X-Semaphore-Preflight', 'Token'].join('-');

export const enhancedComputed = {
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

export const enhancedMethods = {
  async save() {
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
    try {
      await this.beforeSave();
      const payload = this.taskSavePayload();
      const signature = JSON.stringify(payload);
      if (!this.executionPreflight || signature !== this.executionPreflightPayloadSignature) {
        try {
          this.executionPreflight = (await axios.post(`/api/project/${this.projectId}/tasks/preflight`, payload)).data;
        } catch (err) {
          if (this.isExecutionPreflightUnavailable(err)) {
            return await this.submitTaskPayload(this.taskStartPayload(payload));
          }
          throw err;
        }
        this.executionPreflightPayloadSignature = signature;
        this.$emit('preflight', this.executionPreflight);
        if ((this.executionPreflight.findings || []).some(({ severity }) => severity === 'denial')) {
          this.formError = this.$t('executionPreflightDenied');
        }
        return null;
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
        this.executionPreflight = fresh;
        this.executionPreflightPayloadSignature = JSON.stringify(this.taskSavePayload());
        this.formError = this.$t('executionPreflightChanged');
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
