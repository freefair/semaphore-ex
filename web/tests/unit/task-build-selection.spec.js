import './setup';
import { expect } from 'chai';
import axios from 'axios';
import TaskForm from '@/components/TaskForm.vue';

describe('deployment build selection', () => {
  let adapter;
  beforeEach(() => { adapter = axios.defaults.adapter; });
  afterEach(() => { axios.defaults.adapter = adapter; });

  it('finds an older build beyond a full page of successful refreshes', async () => {
    const urls = [];
    axios.defaults.adapter = async (config) => {
      urls.push(config.url);
      const firstPage = urls.length === 1;
      return {
        config,
        status: 200,
        headers: { 'x-has-next': firstPage ? 'true' : 'false' },
        data: firstPage ? Array.from({ length: 20 }, (_, n) => ({
          id: 30 - n, status: 'success', version: 'legacy', params: { inventory_refresh: true },
        })) : [
          { id: 10, status: 'success', params: { inventory_refresh: true } },
          { id: 9, status: 'success', version: '7.3.1' },
        ],
      };
    };
    const builds = await TaskForm.methods.loadBuildTasks.call({
      projectId: 1, template: { build_template_id: 2 },
    });
    expect(builds.map((task) => task.id)).to.deep.equal([9]);
    expect(urls).to.have.length(2);
    expect(urls[0]).to.equal('/api/project/1/templates/2/tasks/last?count=20');
    expect(urls[1]).to.contain('&before=11');
  });

  it('terminates when all available tasks are refreshes', async () => {
    axios.defaults.adapter = async (config) => ({
      config,
      status: 200,
      headers: { 'x-has-next': 'false' },
      data: [{ id: 1, status: 'success', params: { inventory_refresh: true } }],
    });
    expect(await TaskForm.methods.loadBuildTasks.call({
      projectId: 1, template: { build_template_id: 2 },
    })).to.deep.equal([]);
  });
});
