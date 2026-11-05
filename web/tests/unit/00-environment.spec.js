import { expect } from 'chai';

describe('browser storage fixture', () => {
  it('supports the storage operations used by language and UI preferences', () => {
    const key = 'semaphore-unit-storage-check';
    localStorage.setItem(key, 'active');
    expect(localStorage.getItem(key)).to.equal('active');
    localStorage.removeItem(key);
    expect(localStorage.getItem(key)).to.equal(null);
  });
});
