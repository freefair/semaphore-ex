import { expect } from 'chai';
import axios from 'axios';
import ScheduleForm from '@/components/ScheduleForm.vue';
import Schedule from '@/views/project/Schedule.vue';

describe('per-schedule timezone UI', () => {
  it('builds searchable browser timezone options with stable fallbacks', () => {
    const options = ScheduleForm.methods.getTimezoneOptions.call({
      timezone: 'America/New_York',
      item: { timezone: 'Europe/Berlin' },
    });

    expect(options).to.include('UTC');
    expect(options).to.include('America/New_York');
    expect(options).to.include('Europe/Berlin');
    expect(options).to.deep.equal([...new Set(options)].sort());
  });

  it('sends the selected timezone to backend validation and keeps its timing result', async () => {
    const previousAdapter = axios.defaults.adapter;
    const calls = [];
    axios.defaults.adapter = async (config) => {
      calls.push(config);
      return {
        data: {
          effective_timezone: 'Europe/Berlin',
          next_run: '2026-03-30T00:30:00Z',
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };
    try {
      const context = {
        projectId: 4,
        type: '',
        item: { cron_format: '30 2 * * *', timezone: 'Europe/Berlin' },
        schedulePreview: null,
        schedulePreviewRequest: 0,
      };
      const error = await ScheduleForm.methods.validateCronFormat.call(context, '30 2 * * *');

      expect(error).to.equal(null);
      expect(calls).to.have.length(1);
      expect(JSON.parse(calls[0].data)).to.deep.include({
        cron_format: '30 2 * * *',
        timezone: 'Europe/Berlin',
      });
      expect(context.schedulePreview.effective_timezone).to.equal('Europe/Berlin');
      expect(context.schedulePreview.next_run).to.equal('2026-03-30T00:30:00Z');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('ignores a stale backend preview', async () => {
    const previousAdapter = axios.defaults.adapter;
    let resolveRequest;
    axios.defaults.adapter = (config) => new Promise((resolve) => {
      resolveRequest = () => resolve({
        data: { effective_timezone: 'UTC', next_run: '2026-01-01T00:00:00Z' },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      });
    });
    try {
      const context = {
        projectId: 4,
        type: '',
        item: { cron_format: '0 0 * * *', timezone: 'UTC' },
        schedulePreview: { effective_timezone: 'Europe/Berlin' },
        schedulePreviewRequest: 0,
      };
      const pending = ScheduleForm.methods.validateCronFormat.call(context, '0 0 * * *');
      context.schedulePreviewRequest += 1;
      resolveRequest();
      await pending;

      expect(context.schedulePreview.effective_timezone).to.equal('Europe/Berlin');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('formats list timing from backend fields in the effective timezone', () => {
    const rendered = Schedule.methods.formatNextRun.call({
      $t: (key) => key,
    }, {
      effective_timezone: 'Europe/Berlin',
      next_run: '2026-01-01T08:30:00Z',
    });

    expect(rendered).to.equal('2026-01-01 09:30');
  });
});
