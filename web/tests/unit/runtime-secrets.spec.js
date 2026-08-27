import { expect } from 'chai';
import EnvironmentForm from '@/components/EnvironmentForm.vue';
import KeyForm from '@/components/KeyForm.vue';
import SecretStorageForm from '@/components/SecretStorageForm.vue';

describe('runtime secret provider component contracts', () => {
  it('initializes Vault as a runtime-only provider with strict connection defaults', () => {
    const item = SecretStorageForm.methods.getNewItem();
    const context = {
      item,
      itemId: 'new',
      itemType: 'vault',
      secretStorageReady: false,
      secretStorage: 'database',
      $set(target, key, value) {
        Reflect.set(target, key, value);
      },
      $nextTick(callback) {
        callback();
      },
    };

    SecretStorageForm.methods.afterLoadData.call(context);

    expect(context.item.params).to.include({
      mount: 'secret',
      auth_method: 'token',
      timeout: '5s',
    });
    expect(context.item.params).not.to.have.property('tls_skip_verify');
    expect(context.item.readonly).to.equal(true);
    expect(context.item.sync_enabled).to.equal(false);
    expect(context.item.sync_paths).to.deep.equal([]);

    context.item.params.auth_method = 'approle';
    expect(SecretStorageForm.computed.runtimeCredentialLabel.call(context))
      .to.equal('AppRole secret ID');
    context.item.params.auth_method = 'kubernetes';
    expect(SecretStorageForm.computed.runtimeCredentialLabel.call(context))
      .to.equal('Kubernetes JWT');
  });

  it('filters provider choices and obeys the execute capability for remote references', () => {
    const activeContext = {
      systemInfo: {
        capabilities: {
          capabilities: [{
            id: 'runtime_secrets',
            state: 'active',
            reason: 'active',
            access: ['read', 'write', 'execute'],
          }],
        },
      },
      secretStorages: [
        {
          id: 9, name: 'Vault', type: 'vault', readonly: true, params: { mount: 'team' },
        },
        {
          id: 10, name: 'Unsupported', type: 'aws_sm', readonly: true, params: {},
        },
      ],
    };
    activeContext.runtimeDecision = KeyForm.computed.runtimeDecision.call(activeContext);

    expect(KeyForm.computed.runtimeCanExecute.call(activeContext)).to.equal(true);
    expect(KeyForm.computed.runtimeSecretStorages.call(activeContext).map(({ id }) => id))
      .to.deep.equal([9]);

    const item = { source_storage_type: 'vault', source_storage_id: 9 };
    const watchContext = {
      ...activeContext,
      item,
      runtimeSecretStorages: KeyForm.computed.runtimeSecretStorages.call(activeContext),
      $set(target, key, value) {
        Reflect.set(target, key, value);
      },
    };
    KeyForm.watch['item.source_storage_id'].handler.call(watchContext, 9);
    expect(item.source_storage_mount).to.equal('team');

    const disabledContext = {
      systemInfo: {
        capabilities: {
          capabilities: [{
            id: 'runtime_secrets',
            state: 'disabled',
            reason: 'disabled_by_admin',
            access: ['read'],
          }],
        },
      },
    };
    disabledContext.runtimeDecision = KeyForm.computed.runtimeDecision.call(disabledContext);
    expect(KeyForm.computed.runtimeCanExecute.call(disabledContext)).to.equal(false);

    const readOnlyContext = {
      systemInfo: {
        capabilities: {
          capabilities: [{
            id: 'runtime_secrets',
            state: 'read_only',
            reason: 'read_only',
            access: ['read', 'execute'],
          }],
        },
      },
    };
    readOnlyContext.runtimeDecision = KeyForm.computed.runtimeDecision.call(readOnlyContext);
    expect(KeyForm.computed.runtimeCanExecute.call(readOnlyContext)).to.equal(true);
    expect(KeyForm.computed.runtimeCanWrite.call(readOnlyContext)).to.equal(false);
  });

  it('keeps runtime-only providers out of the managed environment sync flow', () => {
    const managed = EnvironmentForm.computed.managedSecretStorages.call({
      secretStorages: [
        { id: 1, type: 'vault' },
        { id: 2, type: 'openbao' },
        { id: 3, type: 'aws_sm' },
      ],
    });

    expect(managed.map(({ id }) => id)).to.deep.equal([3]);
  });
});
