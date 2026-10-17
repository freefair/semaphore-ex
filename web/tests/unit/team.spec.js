import './setup';
import { expect } from 'chai';
import EventBus from '@/event-bus';
import mockAxios, { httpError } from './helpers/axiosMock';

describe('Team.vue', () => {
  let Team;
  let http;

  before(() => {
    // eslint-disable-next-line global-require
    Team = require('@/views/project/Team.vue').default;
  });

  beforeEach(() => {
    http = mockAxios();
  });

  afterEach(() => {
    http.restore();
  });

  const ctx = (extra) => ({
    projectId: 1,
    roles: [
      {
        slug: 'owner', name: 'Owner', builtin: true, project_id: null,
      },
    ],
    loaded: 0,
    saving: Promise.resolve(),
    pendingSaves: 0,
    async loadItems() { this.loaded += 1; },
    $i18n: { t: (key) => key },
    ...extra,
  });

  it('saves the roles of a member', async () => {
    const c = ctx();
    await Team.methods.updateProjectUser.call(c, { id: 5, revision: 4, roles: ['owner', 'guest'] });
    expect(http.requests).to.have.length(1);
    expect(http.requests[0].method).to.equal('put');
    expect(http.requests[0].url).to.equal('/api/project/1/users/5');
    expect(http.requests[0].data).to.deep.equal({ roles: ['owner', 'guest'], revision: 4 });
    expect(c.loaded).to.equal(1);
  });

  it('does not save an empty list', async () => {
    const shown = [];
    const listener = (e) => shown.push(e.text);
    EventBus.$on('i-snackbar', listener);
    const c = ctx();

    await Team.methods.updateProjectUser.call(c, { id: 5, revision: 4, roles: [] });

    EventBus.$off('i-snackbar', listener);
    expect(http.requests).to.have.length(0);
    expect(shown).to.deep.equal(['memberNeedsRole']);
    expect(c.loaded).to.equal(1);
  });

  it('shows the server refusal', async () => {
    const shown = [];
    const listener = (e) => shown.push(e.text);
    EventBus.$on('i-snackbar', listener);
    http.respond(() => {
      throw httpError(400, { error: 'The project needs at least one Owner' });
    });
    const c = ctx();

    await Team.methods.updateProjectUser.call(c, { id: 5, revision: 4, roles: ['guest'] });

    EventBus.$off('i-snackbar', listener);
    expect(shown).to.deep.equal(['The project needs at least one Owner']);
    expect(c.loaded).to.equal(1);
  });

  it('saves quick picks one after another', async () => {
    const tick = () => new Promise((resolve) => { setTimeout(resolve); });
    let release;
    http.respond(() => (http.requests.length === 1
      ? new Promise((resolve) => { release = resolve; })
      : {}));
    const c = ctx();
    const member = { id: 5, revision: 4, roles: ['owner'] };

    const first = Team.methods.updateProjectUser.call(c, member);
    await tick();
    member.roles = ['owner', 'guest'];
    const second = Team.methods.updateProjectUser.call(c, member);
    await tick();
    expect(http.requests).to.have.length(1);

    release({});
    await Promise.all([first, second]);
    expect(http.requests.map((r) => r.data.roles)).to.deep.equal([['owner'], ['owner', 'guest']]);
    expect(http.requests.map((r) => r.data.revision)).to.deep.equal([4, 5]);
    expect(c.loaded).to.equal(1);
  });

  it('captures rapid picks before either queued request starts', async () => {
    const c = ctx();
    const member = { id: 5, revision: 4, roles: ['manager'] };
    const first = Team.methods.updateProjectUser.call(c, member);
    member.roles.push('guest');
    const second = Team.methods.updateProjectUser.call(c, member);
    await Promise.all([first, second]);
    expect(http.requests.map((r) => r.data)).to.deep.equal([
      { roles: ['manager'], revision: 4 },
      { roles: ['manager', 'guest'], revision: 5 },
    ]);
  });

  it('does not advance the observed revision after a refused save', async () => {
    const c = ctx();
    const member = { id: 5, revision: 4, roles: ['guest'] };
    http.respond(() => { throw httpError(409, { error: 'stale membership' }); });
    await Team.methods.updateProjectUser.call(c, member);
    expect(member.revision).to.equal(4);
    expect(c.loaded).to.equal(1);
  });

  it('shows the union of all assigned role permissions', () => {
    const c = ctx({
      roles: [
        { slug: 'run', permissions: 1 },
        { slug: 'edit', permissions: 4 },
      ],
    });
    expect(Team.methods.memberPermissions.call(c, { roles: ['run', 'edit'] })).to.equal(5);
  });

  it('keeps a role missing from the list', () => {
    const items = Team.methods.roleItems.call(ctx(), { roles: ['owner', 'deployer'] });
    expect(items.filter((i) => i.slug).map((i) => i.slug)).to.deep.equal(['owner', 'deployer']);
    expect(items.find((i) => i.slug === 'deployer').name).to.equal('deployer');
  });
});
