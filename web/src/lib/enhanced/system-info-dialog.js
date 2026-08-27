import { capabilityStateColor, findCapabilityDecision } from '@/lib/capabilities';

export const enhancedComputed = {
  lifecycleDecision() {
    return findCapabilityDecision(this.systemInfo, 'lifecycle_test');
  },
  structuredLogs() {
    return this.info?.structured_logs || null;
  },
};

export const enhancedMethods = {
  capabilityStateColor,
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
};
