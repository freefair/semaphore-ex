import { expect } from 'chai';
import uniqueById from '@/lib/enhanced/collections';

describe('template inventory options', () => {
  it('lists an inventory once when the template and project lists both contain it', () => {
    const templateScoped = [{ id: 1, name: 'localhost' }];
    const project = [{ id: 1, name: 'localhost' }, { id: 2, name: 'prod' }];

    const ids = uniqueById([...templateScoped, ...project]).map((item) => item.id);
    expect(ids).to.deep.equal([1, 2]);
  });

  it('accepts missing lists', () => {
    expect(uniqueById(undefined)).to.deep.equal([]);
  });
});
