import axios from 'axios';
import { capabilityStateColor, findCapabilityDecision } from '@/lib/capabilities';

export const enhancedComputed = {
  lifecycleDecision() {
    return findCapabilityDecision(this.systemInfo, 'lifecycle_test');
  },
  totpDecision() {
    return findCapabilityDecision(this.systemInfo, 'totp');
  },
  structuredLogs() {
    return this.info?.structured_logs || null;
  },
  debugFilter() {
    return this.info?.debug_filter || null;
  },
};

export const enhancedMethods = {
  capabilityStateColor,
  async loadTotpRollout() {
    if (!this.totpDecision || this.totpDecision.state === 'unavailable') return;
    this.totpRolloutError = null;
    try {
      const [configuration, users, transitions] = await Promise.all([
        axios.get('/api/capabilities/totp'),
        axios.get('/api/users'),
        axios.get('/api/capabilities/totp/transitions'),
      ]);
      this.totpRollout = configuration.data;
      this.totpUsers = (users.data || []).filter((user) => !user.external);
      this.totpTransitions = transitions.data || [];
    } catch (error) {
      this.totpRolloutError = error.response?.data?.error || error.message;
    }
  },
  async saveTotpRollout() {
    this.totpRolloutSaving = true;
    this.totpRolloutError = null;
    try {
      await axios.put('/api/capabilities/totp', this.totpRollout);
      await this.loadTotpRollout();
      this.$emit('totp-rollout-updated');
    } catch (error) {
      this.totpRolloutError = error.response?.data?.error || error.message;
    } finally {
      this.totpRolloutSaving = false;
    }
  },
  structuredLogStateColor(state) {
    return {
      healthy: 'success',
      disabled: 'grey',
      dropping: 'warning',
      failed: 'error',
    }[state] || 'grey';
  },
  structuredLogStateText(state) {
    return {
      healthy: 'All configured destinations are writable.',
      disabled: 'No structured log destinations are active.',
      dropping: 'The bounded queue has discarded records.',
      failed: 'A destination or flush operation failed.',
    }[state] || 'Writer state is unknown.';
  },
  structuredLogRetention(destination) {
    const parts = [];
    if (destination.max_size_megabytes) parts.push(`${destination.max_size_megabytes} MB`);
    if (destination.max_age_days) {
      parts.push(`${destination.max_age_days} ${destination.max_age_days === 1 ? 'day' : 'days'}`);
    }
    if (destination.max_backups) {
      parts.push(`${destination.max_backups} ${destination.max_backups === 1 ? 'backup' : 'backups'}`);
    }
    if (destination.compress) parts.push('gzip');
    return parts.join(' · ') || 'unlimited';
  },
  structuredLogFailureText(diagnostics) {
    return diagnostics.last_write_error || 'The writer could not access its destination.';
  },
  formatStructuredLogTime(value) {
    if (!value) return 'Never';
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return value;
    return parsed.toLocaleString();
  },
  debugFilterDefaultText(diagnostics) {
    if (diagnostics.default === 'all') return 'All components are captured by default.';
    return 'No component filters are configured.';
  },
  debugFilterRejectedText(entry) {
    return `${entry.entry} — ${entry.reason.replace(/_/g, ' ')}`;
  },
};
