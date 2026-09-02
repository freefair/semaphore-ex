import { expect } from 'chai';
import axios from 'axios';
import Templates from '@/views/project/Templates.vue';

describe('template search UI', () => {
  it('builds case-insensitive safe text segments without interpreting markup', () => {
    const segments = Templates.methods.highlightTemplateSearch.call(
      { appliedTemplateSearch: '<script>' },
      'safe <SCRIPT> value',
    );

    expect(segments).to.deep.equal([
      { text: 'safe ', match: false },
      { text: '<SCRIPT>', match: true },
      { text: ' value', match: false },
    ]);
  });

  it('identifies description, playbook, and tag matches deterministically', () => {
    const context = {
      appliedTemplateSearch: 'blue',
      $t: (key) => key,
    };
    context.templateSearchFields = Templates.methods.templateSearchFields.bind(context);

    const description = Templates.methods.templateSearchSecondaryMatch.call(context, {
      name: 'Deploy', description: 'Blue environment', playbook: 'blue.yml', runner_tags: ['blue'],
    });
    expect(description.field).to.equal('description');

    context.appliedTemplateSearch = 'site.yml';
    const playbook = Templates.methods.templateSearchSecondaryMatch.call(context, {
      name: 'Deploy', description: '', playbook: 'playbooks/site.yml', runner_tags: ['linux'],
    });
    expect(playbook.field).to.equal('playbook');

    context.appliedTemplateSearch = 'gpu';
    const tags = Templates.methods.templateSearchSecondaryMatch.call(context, {
      name: 'Deploy', description: '', playbook: 'site.yml', runner_tags: ['linux', 'GPU'],
    });
    expect(tags.field).to.equal('tags');
  });

  it('debounces into a URL-backed query and resets the table page', async () => {
    let replacement = null;
    let loads = 0;
    const context = {
      templateSearchInput: '  deploy  ',
      appliedTemplateSearch: '',
      templateSearchTimer: null,
      templateTablePage: 4,
      $route: { query: { keep: 'yes', page: '4' } },
      $router: { push: async (value) => { replacement = value; } },
      loadItems: async () => { loads += 1; },
    };
    context.normalizeTemplateSearch = Templates.methods.normalizeTemplateSearch.bind(context);
    context.cancelQueuedTemplateSearch = Templates.methods.cancelQueuedTemplateSearch.bind(context);

    await Templates.methods.applyTemplateSearch.call(context);

    expect(context.appliedTemplateSearch).to.equal('deploy');
    expect(context.templateTablePage).to.equal(1);
    expect(replacement).to.deep.equal({ query: { keep: 'yes', search: 'deploy' } });
    expect(loads).to.equal(1);
  });

  it('cancels a stale request and sends only the applied literal query', async () => {
    const previousAdapter = axios.defaults.adapter;
    const calls = [];
    axios.defaults.adapter = async (config) => {
      calls.push(config);
      return {
        data: [{ id: 2, name: 'Deploy' }],
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      };
    };
    try {
      const stale = new AbortController();
      const context = {
        templateSearchAbort: stale,
        templateSearchRequest: 0,
        templateSearchLoading: false,
        templateSearchError: null,
        appliedTemplateSearch: '100%_literal',
        items: [],
        openedItems: [{ id: 9 }],
        getItemsUrl: () => '/api/project/3/templates',
      };
      context.cancelTemplateSearchRequest = Templates.methods.cancelTemplateSearchRequest
        .bind(context);

      await Templates.methods.loadItems.call(context);

      expect(stale.signal.aborted).to.equal(true);
      expect(calls).to.have.length(1);
      expect(calls[0].params).to.deep.equal({ search: '100%_literal' });
      expect(context.items).to.deep.equal([{ id: 2, name: 'Deploy' }]);
      expect(context.openedItems).to.deep.equal([]);
      expect(context.templateSearchLoading).to.equal(false);
    } finally {
      axios.defaults.adapter = previousAdapter;
    }
  });

  it('preserves the search query while switching existing views', () => {
    const context = {
      projectId: 3,
      appliedTemplateSearch: 'deploy',
      $route: { query: { search: 'deploy', keep: 'yes' } },
    };
    expect(Templates.methods.getViewUrl.call(context, 7)).to.deep.equal({
      path: '/project/3/views/7/templates',
      query: { search: 'deploy', keep: 'yes' },
    });
  });
});
