export const enhancedComputed = {
  inputTimezone() {
    return this.item?.timezone || this.timezone || 'UTC';
  },
  effectiveTimezone() {
    return this.schedulePreview?.effective_timezone
        || this.item?.effective_timezone
        || this.inputTimezone;
  },
};

export const enhancedMethods = {
  getTimezoneOptions() {
    let browserTimezones = [];
    if (typeof Intl.supportedValuesOf === 'function') {
      try {
        browserTimezones = Intl.supportedValuesOf('timeZone');
      } catch {
        browserTimezones = [];
      }
    }
    return [...new Set([
      'UTC',
      this.timezone,
      this.item?.timezone,
      ...browserTimezones,
    ].filter(Boolean))].sort();
  },
  queueRunAtPreview() {
    if (this.runAtPreviewTimer != null) {
      clearTimeout(this.runAtPreviewTimer);
    }
    this.runAtPreviewTimer = setTimeout(async () => {
      this.runAtPreviewTimer = null;
      await this.refreshRunAtPreview();
    }, 250);
  },
  async onScheduleTimezoneChange() {
    this.timezoneOptions = this.getTimezoneOptions();
    if (this.type === 'run_at') {
      await this.refreshRunAtPreview();
    } else {
      await this.refreshCheckboxes();
    }
  },
};
