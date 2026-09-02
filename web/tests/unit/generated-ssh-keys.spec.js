import { expect } from 'chai';
import axios from 'axios';
import KeyForm from '@/components/KeyForm.vue';
import Keys from '@/views/project/Keys.vue';
import ObjectRefsView from '@/components/ObjectRefsView.vue';

describe('generated SSH key UI', () => {
  it('suppresses the delete-only reference warning only when requested', () => {
    expect(ObjectRefsView.props.hideWarning.default).to.equal(false);
    expect(Keys.components.ObjectRefsView).to.equal(ObjectRefsView);
  });

  it('uses the narrow server-generation payload and leaves imported defaults unchanged', () => {
    const item = KeyForm.methods.getNewItem();
    expect(item).not.to.have.property('generate_ssh_key');
    expect(item).not.to.have.property('algorithm');

    const payload = KeyForm.methods.generatedSSHKeyRequest.call({
      item: { name: 'Deployment', ssh: { login: 'deploy', private_key: 'must-not-send' } },
      generatedSSHKeyAlgorithm: 'ed25519',
    });
    expect(payload).to.deep.equal({ name: 'Deployment', login: 'deploy', algorithm: 'ed25519' });
    expect(payload).not.to.have.any.keys('private_key', 'passphrase', 'type', 'project_id', 'source_storage_type');
  });

  it('resets generation when the type or storage becomes unsupported and clears hidden secrets immediately', () => {
    const context = {
      item: { ssh: { private_key: 'private', passphrase: 'passphrase' } },
      generateSSHKey: true,
      generatedSSHKeyAlgorithm: 'rsa-3072',
      $set(target, key, value) { Reflect.set(target, key, value); },
    };
    context.clearGeneratedSSHKeyInput = KeyForm.methods.clearGeneratedSSHKeyInput.bind(context);
    KeyForm.watch.generateSSHKey.call(context, true);
    expect(context.item.ssh.private_key).to.equal('');
    expect(context.item.ssh.passphrase).to.equal('');
    KeyForm.watch['item.type'].call(context, 'login_password');
    expect(context.generateSSHKey).to.equal(false);

    context.generateSSHKey = true;
    KeyForm.computed.sourceStorageTypeIndex.set.call(context, 2);
    expect(context.generateSSHKey).to.equal(false);
    expect(context.item.source_storage_type).to.equal('env');
    KeyForm.methods.afterReset.call(context);
    expect(context.generatedSSHKeyAlgorithm).to.equal('ed25519');
  });

  it('shows only validated generated metadata and never stores private result fields', () => {
    const metadata = KeyForm.computed.generatedSSHKeyMetadata.call({
      item: {
        generated_ssh_key: {
          public_key: 'ssh-ed25519 AAAApublic', fingerprint: 'SHA256:public', algorithm: 'ed25519',
        },
      },
    });
    expect(metadata.public_key).to.equal('ssh-ed25519 AAAApublic');
    expect(KeyForm.computed.generatedSSHKeyMetadata.call({
      item: {
        generated_ssh_key: {
          public_key: 'ssh-rsa invalid', fingerprint: 'SHA256:x', algorithm: 'ed25519',
        },
      },
    })).to.equal(null);

    const context = { generatedKeyDialog: false, generatedKeyResult: null };
    Keys.methods.showGeneratedKeyResult.call(context, {
      public_key: 'ssh-ed25519 AAAApublic',
      fingerprint: 'SHA256:public',
      algorithm: 'ed25519',
      private_key: 'must-not-retain',
    });
    expect(context.generatedKeyResult).to.equal(null);
    Keys.methods.showGeneratedKeyResult.call(context, {
      public_key: 'ssh-ed25519 AAAApublic', fingerprint: 'SHA256:public', algorithm: 'ed25519',
    });
    expect(context.generatedKeyResult).to.deep.equal({
      public_key: 'ssh-ed25519 AAAApublic', fingerprint: 'SHA256:public', algorithm: 'ed25519',
    });
    Keys.watch.generatedKeyDialog.call(context, false);
    expect(context.generatedKeyResult).to.equal(null);
  });

  it('submits server generation with only the allowed payload and clears hidden fields', async () => {
    const previousAdapter = axios.defaults.adapter;
    const calls = [];
    const emitted = [];
    axios.defaults.adapter = async (config) => {
      calls.push(config);
      return {
        data: {
          key: { id: 8 },
          public_key: 'ssh-ed25519 AAAApublic',
          fingerprint: 'SHA256:public',
          algorithm: 'ed25519',
        },
        status: 201,
        statusText: 'Created',
        headers: {},
        config,
      };
    };
    try {
      const context = {
        formError: null,
        formSaving: false,
        projectId: 4,
        item: {
          name: 'Deployment',
          ssh: { login: 'deploy', private_key: 'hidden', passphrase: 'hidden' },
        },
        generatedSSHKeyAlgorithm: 'ed25519',
        $refs: { form: { validate: () => true } },
        $set(target, key, value) { Reflect.set(target, key, value); },
        $emit(event, value) { emitted.push({ event, value }); },
      };
      context.generatedSSHKeyRequest = KeyForm.methods.generatedSSHKeyRequest.bind(context);
      context.clearGeneratedSSHKeyInput = KeyForm.methods.clearGeneratedSSHKeyInput.bind(context);

      await KeyForm.methods.saveGeneratedSSHKey.call(context);

      expect(calls).to.have.length(1);
      expect(calls[0].method).to.equal('post');
      expect(calls[0].url).to.equal('/api/project/4/keys/generate');
      expect(JSON.parse(calls[0].data)).to.deep.equal({
        name: 'Deployment', login: 'deploy', algorithm: 'ed25519',
      });
      expect(context.item.ssh.private_key).to.equal('');
      expect(context.item.ssh.passphrase).to.equal('');
      expect(emitted[0].event).to.equal('save');
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('offers rotation only for authorized local, non-synchronized SSH keys and fetches refs before posting', async () => {
    const can = () => true;
    const permissions = { manageProjectResources: 'manage' };
    expect(Keys.methods.canRotateSSHKey.call({ can, USER_PERMISSIONS: permissions }, { type: 'ssh', synchronized: false })).to.equal(true);
    expect(Keys.methods.canRotateSSHKey.call({ can, USER_PERMISSIONS: permissions }, { type: 'ssh', source_storage_type: 'env' })).to.equal(false);
    expect(Keys.methods.canRotateSSHKey.call({ can, USER_PERMISSIONS: permissions }, { type: 'ssh', synchronized: true })).to.equal(false);
    expect(Keys.methods.canRotateSSHKey.call({ can: () => false, USER_PERMISSIONS: permissions }, { type: 'ssh' })).to.equal(false);
    const previousAdapter = axios.defaults.adapter;
    const calls = [];
    axios.defaults.adapter = async (config) => {
      calls.push(config);
      return {
        data: config.method === 'get' ? { templates: [] } : {
          public_key: 'ssh-rsa AAAApublic', fingerprint: 'SHA256:public', algorithm: 'rsa-3072',
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };
    try {
      const context = {
        can,
        USER_PERMISSIONS: permissions,
        projectId: 4,
        rotationLoading: false,
        rotationError: null,
        rotationRefs: null,
        rotationItem: null,
        rotationDialog: false,
        rotationAlgorithm: 'ed25519',
        loadItems: async () => {},
        showGeneratedKeyResult() {},
      };
      context.canRotateSSHKey = Keys.methods.canRotateSSHKey.bind(context);
      await Keys.methods.beginSSHKeyRotation.call(context, {
        id: 8,
        type: 'ssh',
        synchronized: false,
        generated_ssh_key: { algorithm: 'rsa-3072' },
      });
      expect(calls).to.have.length(1);
      expect(calls[0].method).to.equal('get');
      expect(calls[0].url).to.equal('/api/project/4/keys/8/refs');
      expect(context.rotationDialog).to.equal(true);
      expect(context.rotationAlgorithm).to.equal('rsa-3072');

      context.rotationRequest = Keys.methods.rotationRequest.bind(context);
      await Keys.methods.rotateSSHKey.call(context);
      expect(calls).to.have.length(2);
      expect(calls[1].method).to.equal('post');
      expect(calls[1].url).to.equal('/api/project/4/keys/8/rotate');
      expect(JSON.parse(calls[1].data)).to.deep.equal({ algorithm: 'rsa-3072', confirm_rotation: true });
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });
});
