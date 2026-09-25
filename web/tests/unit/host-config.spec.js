import './setup';
import { expect } from 'chai';
import HostConfigForm from '@/components/HostConfigForm.vue';

describe('host configuration form lifecycle', () => {
  it('accepts a cleared item when its dialog is cancelled or reset', () => {
    expect(() => HostConfigForm.watch['item.type'].call({
      keys: [{ id: 1, type: 'ssh' }],
      item: null,
    })).not.to.throw();
  });

  it('clears a login/password selection when switching to an SSH host', () => {
    const context = {
      keys: [{ id: 1, type: 'ssh' }, { id: 2, type: 'login_password' }],
      item: { type: 'host', ssh_key_id: 2 },
      isHost: true,
    };
    context.credentials = HostConfigForm.computed.credentials.call(context);
    HostConfigForm.watch['item.type'].call(context);
    expect(context.item.ssh_key_id).to.equal(null);
  });
});
