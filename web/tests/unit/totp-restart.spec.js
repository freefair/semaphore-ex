import { expect } from 'chai';
import TotpEnrollmentPanel from '@/components/TotpEnrollmentPanel.vue';

function panel(state) {
  const context = {
    ...TotpEnrollmentPanel.data.call({}),
    status: { enrollment_state: state },
    begun: 0,
  };
  context.beginEnrollment = () => { context.begun += 1; context.restartConfirmation = false; };
  context.requestEnrollment = TotpEnrollmentPanel.methods.requestEnrollment.bind(context);
  return context;
}

describe('TOTP enrollment restart', () => {
  it('starts a first setup directly', () => {
    const context = panel('none');
    context.requestEnrollment();
    expect(context.begun).to.equal(1);
    expect(context.restartConfirmation).to.equal(false);
  });

  it('asks in the page before replacing a pending setup', () => {
    const context = panel('pending_confirmation');
    context.requestEnrollment();
    expect(context.begun).to.equal(0);
    expect(context.restartConfirmation).to.equal(true);
  });
});
