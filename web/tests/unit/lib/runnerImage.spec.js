import { expect } from 'chai';
import { RUNNER_IMAGE, runnerImageTag } from '@/lib/enhanced/runner-image';

describe('runner image reference', () => {
  it('points at the fork runner image', () => {
    expect(RUNNER_IMAGE).to.equal('ghcr.io/freefair/semaphore-ex-runner');
  });

  it('keeps the full fork release tag from the server version string', () => {
    expect(runnerImageTag('v2.20.0-ex.1-2c724bfe-1757694144')).to.equal('v2.20.0-ex.1');
  });

  it('keeps release candidate tags', () => {
    expect(runnerImageTag('v2.20.0-ex.1-rc1-2c724bfe-1757694144')).to.equal('v2.20.0-ex.1-rc1');
  });

  it('accepts plain upstream-style tags', () => {
    expect(runnerImageTag('v2.19.14-abcdef01-1700000000')).to.equal('v2.19.14');
  });

  it('falls back to latest for development builds', () => {
    expect(runnerImageTag('undefined-00000000-')).to.equal('latest');
    expect(runnerImageTag('develop-abcdef01-1700000000')).to.equal('latest');
    expect(runnerImageTag('')).to.equal('latest');
    expect(runnerImageTag(undefined)).to.equal('latest');
  });
});
