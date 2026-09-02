import dayjs from 'dayjs';

const enhancedMethods = {
  formatNextRun(item) {
    if (!item.next_run) {
      return '—';
    }
    const tz = item.effective_timezone || this.systemInfo?.schedule_timezone || 'UTC';
    const parsed = dayjs(item.next_run).tz(tz);
    return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm') : '—';
  },
};

export default enhancedMethods;
