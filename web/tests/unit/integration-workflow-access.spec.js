import './setup';
import { expect } from 'chai';
import IntegrationForm from '@/components/IntegrationForm.vue';
import Integrations from '@/views/project/Integrations.vue';
import mockAxios, { httpError } from './helpers/axiosMock';

[['IntegrationForm', IntegrationForm], ['Integrations', Integrations]].forEach(([name, component]) => {
  describe(`${name} workflow visibility`, () => {
    let http;
    beforeEach(() => { http = mockAxios(); });
    afterEach(() => { http.restore(); });
    it('keeps template integrations available when workflow listing is forbidden', async () => {
      http.respond((config) => {
        if (config.url.endsWith('/workflows')) throw httpError(403, {});
        return [{ id: 11, name: 'Deploy' }];
      });
      const ctx = { projectId: 7, features: { workflows: true }, workflowsEnabled: true };
      await component.created.call(ctx);
      expect(ctx.templates).to.deep.equal([{ id: 11, name: 'Deploy' }]);
      expect(ctx.workflows).to.deep.equal([]);
    });
    it('does not hide workflow server failures as missing permission', async () => {
      http.respond((config) => {
        if (config.url.endsWith('/workflows')) throw httpError(500, {});
        return [];
      });
      const ctx = { projectId: 7, features: { workflows: true }, workflowsEnabled: true };
      let error;
      try {
        await component.created.call(ctx);
      } catch (caught) {
        error = caught;
      }
      expect(error.response.status).to.equal(500);
    });
  });
});
