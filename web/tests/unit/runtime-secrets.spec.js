import { expect } from 'chai';
import EnvironmentForm from '@/components/EnvironmentForm.vue';
import KeyForm from '@/components/KeyForm.vue';
import SecretStorageForm from '@/components/SecretStorageForm.vue';
import SecretStorageSyncOptionsForm from '@/components/SecretStorageSyncOptionsForm.vue';
import SecretStorages from '@/views/project/SecretStorages.vue';

describe('runtime secret provider component contracts', () => {
  it('initializes Vault as a read-only provider with strict connection defaults', async () => {
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
      async loadProjectResources() {
        return [];
      },
    };

    await SecretStorageForm.methods.afterLoadData.call(context);

    expect(context.item.params).to.include({
      mount: 'secret',
      auth_method: 'token',
      timeout: '5s',
    });
    expect(context.item.params).not.to.have.property('tls_skip_verify');
    expect(context.item.readonly).to.equal(true);
    expect(context.item.sync_direction).to.equal('read_only');
    expect(context.item.sync_enabled).to.equal(false);
    expect(context.item.sync_paths).to.deep.equal([]);

    context.item.params.auth_method = 'approle';
    expect(SecretStorageForm.computed.runtimeCredentialLabel.call(context)).to.equal(
      'AppRole secret ID',
    );
    context.item.params.auth_method = 'kubernetes';
    expect(SecretStorageForm.computed.runtimeCredentialLabel.call(context)).to.equal(
      'Kubernetes JWT',
    );
  });

  it('preserves explicit outbound mappings and filters selectable local keys', async () => {
    const item = {
      type: 'openbao',
      params: { mount: 'team' },
      sync_direction: 'outbound',
      sync_enabled: false,
      sync_paths: [{
        access_key_id: 7, mount: 'team', path: 'apps/api', field: 'token',
      }],
    };
    const context = {
      item,
      itemId: 9,
      itemType: 'openbao',
      secretStorageReady: false,
      secretStorage: 'database',
      $set(target, key, value) {
        Reflect.set(target, key, value);
      },
      $nextTick(callback) {
        callback();
      },
      async loadProjectResources() {
        return [
          { id: 7, name: 'Local', type: 'string' },
          {
            id: 8, name: 'Remote', type: 'string', source_storage_type: 'vault',
          },
          {
            id: 9, name: 'Environment-owned', type: 'string', owner: 'environment',
          },
        ];
      },
    };

    await SecretStorageForm.methods.afterLoadData.call(context);

    expect(item.readonly).to.equal(false);
    expect(item.sync_paths).to.have.length(1);
    expect(
      SecretStorageForm.computed.managedLocalKeys.call(context).map(({ id }) => id),
    ).to.deep.equal([7]);
    expect(
      SecretStorageForm.computed.isManagedOutbound.call({
        item,
        isRuntimeProvider: true,
      }),
    ).to.equal(true);
  });

  it('creates explicit managed target mappings and summarizes operation state', () => {
    const emitted = [];
    const mappingContext = {
      managed: true,
      defaultMount: 'team',
      paths: [],
      $emit(event, value) {
        emitted.push({ event, value });
      },
    };
    mappingContext.emitPaths = SecretStorageSyncOptionsForm.methods.emitPaths.bind(mappingContext);

    SecretStorageSyncOptionsForm.methods.addPath.call(mappingContext);

    expect(mappingContext.paths).to.deep.equal([
      {
        access_key_id: null, mount: 'team', path: '', field: '',
      },
    ]);
    expect(emitted[0].event).to.equal('input');

    const historyContext = {
      localKeys: [{ id: 7, name: 'Deployment key' }],
    };
    expect(SecretStorages.methods.keyName.call(historyContext, 7)).to.equal('Deployment key');
    expect(SecretStorages.methods.keyName.call(historyContext, 8)).to.equal('#8');

    const headers = SecretStorages.methods.getHeaders.call({
      $i18n: { t: (value) => value },
      $vuetify: { breakpoint: { smAndDown: true } },
    });
    expect(headers.map(({ value }) => value)).to.deep.equal(['name', 'actions']);
  });

  it('filters provider choices and obeys the execute capability for remote references', () => {
    const activeContext = {
      systemInfo: {
        capabilities: {
          capabilities: [
            {
              id: 'runtime_secrets',
              state: 'active',
              reason: 'active',
              access: ['read', 'write', 'execute'],
            },
          ],
        },
      },
      secretStorages: [
        {
          id: 9,
          name: 'Vault',
          type: 'vault',
          readonly: true,
          params: { mount: 'team' },
        },
        {
          id: 10,
          name: 'Unsupported',
          type: 'aws_sm',
          readonly: true,
          params: {},
        },
      ],
    };
    activeContext.runtimeDecision = KeyForm.computed.runtimeDecision.call(activeContext);

    expect(KeyForm.computed.runtimeCanExecute.call(activeContext)).to.equal(true);
    expect(
      KeyForm.computed.runtimeSecretStorages.call(activeContext).map(({ id }) => id),
    ).to.deep.equal([9]);

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
          capabilities: [
            {
              id: 'runtime_secrets',
              state: 'disabled',
              reason: 'disabled_by_admin',
              access: ['read'],
            },
          ],
        },
      },
    };
    disabledContext.runtimeDecision = KeyForm.computed.runtimeDecision.call(disabledContext);
    expect(KeyForm.computed.runtimeCanExecute.call(disabledContext)).to.equal(false);

    const readOnlyContext = {
      systemInfo: {
        capabilities: {
          capabilities: [
            {
              id: 'runtime_secrets',
              state: 'read_only',
              reason: 'read_only',
              access: ['read', 'execute'],
            },
          ],
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
