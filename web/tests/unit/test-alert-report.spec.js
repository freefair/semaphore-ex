import { expect } from 'chai';
import { mount } from '@vue/test-utils';
import TestAlertReport from '@/components/enhanced/TestAlertReport.vue';

function mountReport(report) {
  return mount(TestAlertReport, {
    props: { report },
    global: { mocks: { $t: (key) => `t:${key}` } },
  });
}

describe('test alert report', () => {
  it('does not claim success when no channel delivered', () => {
    const wrapper = mountReport({
      sent: false,
      channels: [{
        channel: 'email',
        configured: false,
        status: 'not_configured',
        recipients: 0,
        config_keys: ['email_alert', 'email_host'],
        detail: '',
      }],
    });

    expect(wrapper.text()).to.contain('t:testAlertNotSentSummary');
    expect(wrapper.text()).not.to.contain('t:testAlertSentSummary');
    const email = wrapper.find('[data-testid="test-alert-email"]');
    expect(email.text()).to.contain('t:testAlertStatusNotConfigured');
    expect(email.findAll('code').map((code) => code.text())).to.deep.equal(['email_alert', 'email_host']);
  });

  it('shows delivery and failure per channel with the failure detail', () => {
    const wrapper = mountReport({
      sent: true,
      channels: [
        {
          channel: 'slack', configured: true, status: 'sent', recipients: 0, config_keys: [], detail: '',
        },
        {
          channel: 'gotify',
          configured: true,
          status: 'failed',
          recipients: 0,
          config_keys: [],
          detail: "Can't send gotify alert! Response code: 500",
        },
      ],
    });

    expect(wrapper.text()).to.contain('t:testAlertSentSummary');
    expect(wrapper.find('[data-testid="test-alert-slack"]').text()).to.contain('t:testAlertStatusSent');
    const gotify = wrapper.find('[data-testid="test-alert-gotify"]');
    expect(gotify.text()).to.contain('t:testAlertStatusFailed');
    expect(gotify.text()).to.contain('Response code: 500');
  });

  it('explains missing e-mail recipients', () => {
    const wrapper = mountReport({
      sent: false,
      channels: [{
        channel: 'email', configured: true, status: 'no_recipients', recipients: 0, config_keys: [], detail: '',
      }],
    });

    expect(wrapper.find('[data-testid="test-alert-email"]').text())
      .to.contain('t:testAlertStatusNoRecipients');
  });
});
