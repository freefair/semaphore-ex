// Container image used by the runner onboarding commands, and the tag that matches the
// running server. Fork builds report versions like v2.20.0-ex.1[-rcM]-<commit>-<date>;
// anything else (development builds) falls back to the latest published image.
export const RUNNER_IMAGE = 'ghcr.io/freefair/semaphore-ex-runner';

const RELEASE_TAG = /^v\d+\.\d+\.\d+(?:-ex\.\d+(?:-(?:rc|beta)\d+)?)?/;

export function runnerImageTag(version) {
  const match = RELEASE_TAG.exec(version || '');
  return match ? match[0] : 'latest';
}
