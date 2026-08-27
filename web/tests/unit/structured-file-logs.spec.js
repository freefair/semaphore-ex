import { expect } from 'chai';
import SystemInfoDialog from '@/components/SystemInfoDialog.vue';

describe('structured file log diagnostics', () => {
  it('maps every writer state to an explicit visual treatment', () => {
    const color = SystemInfoDialog.methods.structuredLogStateColor;
    const text = SystemInfoDialog.methods.structuredLogStateText;

    expect(color('healthy')).to.equal('success');
    expect(color('disabled')).to.equal('grey');
    expect(color('dropping')).to.equal('warning');
    expect(color('failed')).to.equal('error');
    ['healthy', 'disabled', 'dropping', 'failed'].forEach((state) => {
      expect(text(state)).to.be.a('string').with.length.greaterThan(0);
    });
  });

  it('reads diagnostics only from the authorized admin response', () => {
    const diagnostics = {
      state: 'dropping',
      queue_depth: 16,
      queue_capacity: 16,
      dropped_records: 4,
    };
    expect(SystemInfoDialog.computed.structuredLogs.call({
      info: { structured_logs: diagnostics },
    })).to.equal(diagnostics);
    expect(SystemInfoDialog.computed.structuredLogs.call({ info: {} })).to.equal(null);
  });

  it('formats effective retention and incomplete flush information', () => {
    expect(SystemInfoDialog.methods.structuredLogRetention({
      max_size_megabytes: 10,
      max_age_days: 7,
      max_backups: 3,
      compress: true,
    })).to.equal('10 MB · 7 days · 3 backups · gzip');
    expect(SystemInfoDialog.methods.structuredLogRetention({
      max_size_megabytes: 1,
      max_age_days: 1,
      max_backups: 1,
      compress: false,
    })).to.equal('1 MB · 1 day · 1 backup');
    expect(SystemInfoDialog.methods.structuredLogRetention({})).to.equal('unlimited');
    expect(SystemInfoDialog.methods.formatStructuredLogTime(null)).to.equal('Never');
  });
});
