import { expect } from 'chai';
import ScheduleForm from '@/components/ScheduleForm.vue';

describe('schedule form defaults and live preview', () => {
  it('starts a new schedule daily at midnight instead of every minute', () => {
    const item = ScheduleForm.methods.getNewItem.call({});

    expect(item.cron_format).to.equal('0 0 * * *');
  });

  it('refreshes the next-run preview whenever the picker changes the expression', () => {
    const validated = [];
    const context = {
      timing: 'daily',
      months: [],
      weekdays: [],
      days: [],
      hours: [2],
      minutes: [0],
      item: { cron_format: '0 0 * * *' },
      validateCronFormat: (cron) => validated.push(cron),
    };

    ScheduleForm.methods.refreshCron.call(context);

    expect(context.item.cron_format).to.equal('0 2 * * *');
    expect(validated).to.deep.equal(['0 2 * * *']);
  });

  it('remembers the raw cron view only when the user switched to it', () => {
    const storage = new Map();
    const previous = globalThis.localStorage;
    globalThis.localStorage = {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, value),
      removeItem: (key) => storage.delete(key),
    };
    try {
      ScheduleForm.watch.rawCron(true);
      expect(storage.get('schedule__raw_cron')).to.equal('raw');
      ScheduleForm.watch.rawCron(false);
      expect(storage.has('schedule__raw_cron')).to.equal(false);
    } finally {
      globalThis.localStorage = previous;
    }
  });
});
