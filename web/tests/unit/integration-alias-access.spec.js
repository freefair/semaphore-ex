import './setup';
import { expect } from 'chai';
import IntegrationsBase from '@/views/project/IntegrationsBase';
import IntegrationExtractor from '@/views/project/IntegrationExtractor.vue';
import mockAxios, { httpError } from './helpers/axiosMock';

describe('integration alias access', () => {
  let http;
  beforeEach(() => { http = mockAxios(); });
  afterEach(() => { http.restore(); });

  it('clears previously visible aliases on permission denial without blocking template management', async () => {
    http.respond(() => { throw httpError(403, {}); });
    const ctx = { aliasPath: '/api/project/7/integrations/aliases', aliases: [{ id: 1 }], aliasesAccessible: true };
    await IntegrationsBase.methods.loadAliases.call(ctx);
    expect(ctx.aliases).to.deep.equal([]);
    expect(ctx.aliasesAccessible).to.equal(false);
  });

  it('does not hide server failure as missing permission', async () => {
    http.respond(() => { throw httpError(500, {}); });
    const ctx = { aliasPath: '/api/project/7/integrations/aliases' };
    let error;
    try { await IntegrationsBase.methods.loadAliases.call(ctx); } catch (caught) { error = caught; }
    expect(error.response.status).to.equal(500);
  });

  it('restores alias access after permissions recover', async () => {
    http.respond(() => [{ id: 5 }]);
    const ctx = { aliasPath: '/api/project/7/integrations/aliases', aliasesAccessible: false };
    await IntegrationsBase.methods.loadAliases.call(ctx);
    expect(ctx.aliases).to.deep.equal([{ id: 5 }]);
    expect(ctx.aliasesAccessible).to.equal(true);
  });

  it('keeps template inputs editable while workflow inputs wait for verified access', () => {
    const readOnly = IntegrationExtractor.computed.inputsReadOnly;
    expect(readOnly.call({
      integration: { template_id: 2 }, aliases: null, aliasesAccessible: false,
    })).to.equal(false);
    expect(readOnly.call({
      integration: { workflow_id: 2 }, aliases: null, aliasesAccessible: true,
    })).to.equal(true);
    expect(readOnly.call({
      integration: { workflow_id: 2 }, aliases: [], aliasesAccessible: false,
    })).to.equal(true);
    expect(readOnly.call({
      integration: { workflow_id: 2 }, aliases: [], aliasesAccessible: true,
    })).to.equal(false);
  });
});
