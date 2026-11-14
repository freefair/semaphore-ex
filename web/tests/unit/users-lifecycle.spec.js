import { expect } from 'chai';
import axios from 'axios';
import Users from '@/views/Users.vue';

function usersContext() {
  const context = {
    ...Users.data.call({}),
    $t: (key, params) => (params ? `${key}:${JSON.stringify(params)}` : key),
    loaded: 0,
    async loadItems() { this.loaded += 1; },
  };
  ['openLifecycleDialog', 'closeLifecycleDialog', 'changeLifecycle'].forEach((name) => {
    context[name] = Users.methods[name].bind(context);
  });
  return context;
}

describe('user deactivation', () => {
  let originalPut;

  beforeEach(() => { originalPut = axios.put; });
  afterEach(() => { axios.put = originalPut; });

  it('deactivates an active user and reloads the list', async () => {
    let request;
    axios.put = async (url, data) => { request = { url, data }; return { status: 204 }; };
    const context = usersContext();
    context.openLifecycleDialog({ id: 5, name: 'Sam Weber', disabled: false });

    await context.changeLifecycle();

    expect(request).to.deep.equal({ url: '/api/users/5/deactivation', data: { disabled: true } });
    expect(context.lifecycleTarget).to.equal(null);
    expect(context.loaded).to.equal(1);
  });

  it('reactivates a deactivated user', async () => {
    let request;
    axios.put = async (url, data) => { request = { url, data }; return { status: 204 }; };
    const context = usersContext();
    context.openLifecycleDialog({ id: 5, name: 'Sam Weber', disabled: true });

    await context.changeLifecycle();

    expect(request.data).to.deep.equal({ disabled: false });
  });

  it('keeps the dialog open with the reason when the last administrator would be locked out', async () => {
    axios.put = async () => {
      const error = new Error('Request failed');
      error.response = { status: 409, data: { error: 'At least one effective administrator must remain active' } };
      throw error;
    };
    const context = usersContext();
    context.openLifecycleDialog({ id: 1, name: 'Admin', disabled: false });

    await context.changeLifecycle();

    expect(context.lifecycleTarget.id).to.equal(1);
    expect(context.lifecycleError).to.equal('At least one effective administrator must remain active');
    expect(context.loaded).to.equal(0);
  });
});
