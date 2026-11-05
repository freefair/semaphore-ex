import { expect } from 'chai';

describe('Keys.vue', () => {
  let Keys;

  beforeAll(async () => {
    Object.defineProperty(global, 'localStorage', {
      configurable: true,
      value: {
        getItem: () => null,
      },
    });
    Object.defineProperty(global, 'navigator', {
      configurable: true,
      value: {
        language: 'en-US',
      },
    });
    Keys = (await import('@/views/project/Keys.vue')).default;
  });

  it('shows a generated public key only from the dedicated response', async () => {
    const received = [];
    const context = {
      loadItems: async () => received.push('reload'),
      showGeneratedKeyResult: (response) => received.push(response),
    };
    const response = {
      public_key: 'ssh-ed25519 AAAApublic',
      fingerprint: 'SHA256:public',
      algorithm: 'ed25519',
    };

    await Keys.methods.onKeySaved.call(context, { action: 'generated', response });

    expect(received).to.deep.equal(['reload', response]);
  });

  it('does not depend on generic generated-key request fields', () => {
    expect(Keys.methods).not.to.have.property('loadItemsAndShowPublicKey');
    expect(Keys.methods).not.to.have.property('extractPublicKey');
  });
});
