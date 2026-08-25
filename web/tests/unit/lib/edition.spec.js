import { expect } from 'chai';
import { isEnhancedEdition, resolveEdition } from '@/lib/edition';

describe('edition', () => {
  it('prefers the server-authoritative edition', () => {
    expect(resolveEdition('community', 'enhanced')).to.equal('community');
    expect(resolveEdition('enhanced', 'community')).to.equal('enhanced');
  });

  it('uses the pinned bundle edition before system info loads', () => {
    expect(isEnhancedEdition(undefined, 'enhanced')).to.equal(true);
    expect(isEnhancedEdition(undefined, 'community')).to.equal(false);
    expect(isEnhancedEdition(undefined, 'unknown')).to.equal(false);
  });
});
