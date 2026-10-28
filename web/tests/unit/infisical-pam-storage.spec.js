import { expect } from 'chai';
import InfisicalPAMStorageFields from '@/components/enhanced/InfisicalPAMStorageFields.vue';
import SecretStorageForm from '@/components/SecretStorageForm.vue';
import SecretStorages from '@/views/project/SecretStorages.vue';

function fieldsContext(routes) {
  const emitted = [];
  return {
    routes,
    get routeList() {
      return InfisicalPAMStorageFields.computed.routeList.call(this);
    },
    $emit(event, value) {
      emitted.push([event, value]);
    },
    emitted,
  };
}

describe('Infisical PAM storage form', () => {
  it('adds, edits and removes routes through explicit updates', () => {
    const context = fieldsContext(undefined);

    InfisicalPAMStorageFields.methods.addRoute.call(context);
    expect(context.emitted[0]).to.deep.equal(['update:routes', [{ host: '', account: '' }]]);

    const routes = [{ host: 'web-1', account: 'servers/web-1' }, { host: 'db-1', account: 'servers/db-1' }];
    const editContext = fieldsContext(routes);
    InfisicalPAMStorageFields.methods.updateRoute.call(editContext, 1, 'host', 'db-2');
    InfisicalPAMStorageFields.methods.removeRoute.call(editContext, 0);

    expect(editContext.emitted[0]).to.deep.equal(['update:routes', [
      { host: 'web-1', account: 'servers/web-1' },
      { host: 'db-2', account: 'servers/db-1' },
    ]]);
    expect(editContext.emitted[1]).to.deep.equal(['update:routes', [{ host: 'db-1', account: 'servers/db-1' }]]);
    expect(routes[1].host).to.equal('db-1');
  });

  it('mirrors the server host and account rules', () => {
    const { hostPattern, accountPattern } = InfisicalPAMStorageFields.data();

    expect(hostPattern.test('web-1.example.com')).to.equal(true);
    expect(hostPattern.test('*.example.com')).to.equal(false);
    expect(hostPattern.test('web\n ProxyCommand sh')).to.equal(false);
    expect(accountPattern.test('servers/web-1')).to.equal(true);
    expect(accountPattern.test('prod/linux/web_1')).to.equal(true);
    expect(accountPattern.test('web-1')).to.equal(false);
    expect(accountPattern.test('servers/')).to.equal(false);
  });

  it('treats Infisical as neither runtime provider nor sync target', () => {
    const context = { item: { type: 'infisical', sync_direction: 'read_only' } };
    context.isRuntimeProvider = SecretStorageForm.computed.isRuntimeProvider.call(context);

    expect(SecretStorageForm.computed.isInfisicalPAM.call(context)).to.equal(true);
    expect(context.isRuntimeProvider).to.equal(false);
    expect(SecretStorageForm.computed.isManagedOutbound.call(context)).to.equal(false);
  });

  it('widens the storage dialog for route editing', () => {
    expect(SecretStorages.computed.dialogMaxWidth.call({ dialogStorageType: 'infisical' })).to.equal(760);
    expect(SecretStorages.computed.dialogMaxWidth.call({ dialogStorageType: 'vault' })).to.equal(450);
    expect(SecretStorages.methods.getIcon('infisical')).to.equal('mdi-shield-key-outline');
  });
});
