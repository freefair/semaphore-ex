import { expect } from 'chai';
import { noneKeyId, preselectedKeyId } from '@/lib/enhanced/default-credentials';

const keys = [{ id: 3, type: 'ssh' }, { id: 1, type: 'none' }];

describe('default credentials for new resources', () => {
  it('finds the project None key', () => {
    expect(noneKeyId(keys)).to.equal(1);
    expect(noneKeyId([{ id: 3, type: 'ssh' }])).to.equal(null);
  });

  it('preselects None only for a new resource without a chosen key', () => {
    expect(preselectedKeyId({ ssh_key_id: null }, keys, 'ssh_key_id', true)).to.equal(1);
    expect(preselectedKeyId({ ssh_key_id: 3 }, keys, 'ssh_key_id', true)).to.equal(null);
    expect(preselectedKeyId({ ssh_key_id: null }, keys, 'ssh_key_id', false)).to.equal(null);
  });
});
