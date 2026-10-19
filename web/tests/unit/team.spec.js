import './setup';
import { expect } from 'chai';
import { mount, createLocalVue } from '@vue/test-utils';
import Vuetify from 'vuetify';
import i18n from '@/plugins/i18';
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

  it('includes mapped role permissions without making them manual roles', () => {
    const c = ctx({ roles: [{ slug: 'run', permissions: 1 }, { slug: 'edit', permissions: 4 }] });
    const member = {
      roles: ['run'], mapped_roles: [{ role: 'edit', provider: 'qa', group: 'operators' }],
    };
    expect(Team.methods.memberPermissions.call(c, member)).to.equal(5);
    expect(member.roles).to.deep.equal(['run']);
    const mappedOnly = { roles: [], mapped_roles: member.mapped_roles };
    expect(Team.methods.memberPermissions.call(c, mappedOnly)).to.equal(4);
  });

  it('keeps a role missing from the list', () => {
    const items = Team.methods.roleItems.call(ctx(), { roles: ['owner', 'deployer'] });
    expect(items.filter((i) => i.slug).map((i) => i.slug)).to.deep.equal(['owner', 'deployer']);
    expect(items.find((i) => i.slug === 'deployer').name).to.equal('deployer');
  });

  it('keeps refusing an empty list without mapped roles', async () => {
    const shown = [];
    const listener = (e) => shown.push(e.text);
    EventBus.$on('i-snackbar', listener);
    const c = ctx();

    await Team.methods.updateProjectUser.call(c, { id: 5, roles: [], mapped_roles: [] });

    EventBus.$off('i-snackbar', listener);
    expect(http.requests).to.have.length(0);
    expect(shown).to.deep.equal(['memberNeedsRole']);
  });

  it('does not save a directory-managed member', async () => {
    const shown = [];
    const listener = (e) => shown.push(e.text);
    EventBus.$on('i-snackbar', listener);
    const c = { ...ctx(), isDirectoryManaged: Team.methods.isDirectoryManaged };
    const mapped = [{ role: 'owner', provider: 'okta', group: 'Admins' }];

    await Team.methods.updateProjectUser.call(c, { id: 5, roles: [], mapped_roles: mapped });

    EventBus.$off('i-snackbar', listener);
    expect(http.requests).to.have.length(0);
    expect(shown).to.deep.equal(['memberKeepsMappedRoles']);
  });

  it('does not delete a directory-managed member', async () => {
    const shown = [];
    const listener = (e) => shown.push(e.text);
    EventBus.$on('i-snackbar', listener);
    const c = {
      ...ctx(),
      items: [],
      isDirectoryManaged: Team.methods.isDirectoryManaged,
      getSingleItemUrl() { return `/api/project/1/users/${this.itemId}`; },
      async loadItems() { this.loaded += 1; },
    };
    c.items = [{ id: 5, roles: [], mapped_roles: [{ role: 'owner' }] }];

    await Team.methods.deleteItem.call(c, 5);

    EventBus.$off('i-snackbar', listener);
    expect(http.requests).to.have.length(0);
    expect(shown).to.deep.equal(['memberKeepsMappedRoles']);
  });

  it('shows mapped roles as locked chips', async () => {
    http.respond((config) => {
      if (config.url === '/api/project/1/roles/all') {
        return [{
          slug: 'owner', name: 'Owner', builtin: true, project_id: null,
        }];
      }
      return [{
        id: 5, name: 'Alice', username: 'alice', roles: [], mapped_roles: [{ role: 'owner', provider: 'okta', group: 'Admins' }],
      }];
    });
    const wrapper = mount(Team, {
      localVue: createLocalVue(),
      vuetify: new Vuetify(),
      i18n,
      propsData: { projectId: 1, systemInfo: { teams: {} } },
      stubs: {
        EditTeamMemberDialog: true,
        YesNoDialog: true,
        TeamMenu: true,
        'v-tooltip': { template: '<div><slot name="activator" :on="{}" :attrs="{}" /><slot /></div>' },
      },
    });
    await new Promise((resolve) => { setTimeout(resolve, 0); });

    const chip = wrapper.find('.v-chip');
    expect(chip.find('.mdi-lock').exists()).to.equal(true);
    expect(chip.text()).to.equal('Owner');
    expect(wrapper.text()).to.contain('okta: Admins');
    wrapper.destroy();
  });
});
