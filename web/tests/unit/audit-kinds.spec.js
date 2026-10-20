import { expect } from 'chai';
import { messages } from '@/lang';
import AUDIT_KINDS from '@/lib/auditKinds';

describe('lib/auditKinds', () => {
  it('has an English title for every kind', () => {
    const missing = AUDIT_KINDS.filter((kind) => !messages.en[`audit_kind_${kind.replace(/[./]/g, '_')}`]);
    expect(missing).to.deep.equal([]);
  });
});
