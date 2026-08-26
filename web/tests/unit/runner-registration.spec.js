import { expect } from 'chai';
import RunnerForm from '@/components/RunnerForm.vue';
import Runners from '@/views/Runners.vue';

describe('project runner registration', () => {
  it('creates project runners only through the one-time registration flow', () => {
    const projectRunner = RunnerForm.methods.getNewItem.call({ projectId: 17 });
    const globalRunner = RunnerForm.methods.getNewItem.call({ projectId: null });

    expect(projectRunner.registered).to.equal(false);
    expect(projectRunner.is_default).to.equal(false);
    expect(globalRunner.registered).to.equal(true);
  });

  it('shows returned registration material and builds copyable register commands', async () => {
    const token = 'smrs_contract-token';
    const context = {
      newRunner: null,
      newRunnerTokenDialog: false,
      registerTab: 2,
      loadItems: async () => [],
      $t: (key) => key,
    };

    await Runners.methods.loadItemsAndShowRunnerDetails.call(context, {
      action: 'new',
      item: { id: 23, project_id: 17, registration_token: token },
    });

    expect(context.newRunnerTokenDialog).to.equal(true);
    expect(context.newRunner.registration_token).to.equal(token);
    const command = Runners.computed.runnerRegisterEnvCommand.call({
      webHost: 'https://semaphore.example',
      newRunner: context.newRunner,
    });
    expect(command).to.include(`SEMAPHORE_RUNNER_REGISTRATION_TOKEN=${token}`);
    expect(command).to.include('semaphore runner register');
    expect(command).to.include('semaphore runner start');
  });

  it('keeps the backend online state in the rendered runner collection', () => {
    const online = { id: 23, status: 'online', registered: true };
    const filtered = Runners.computed.filteredItems.call({
      items: [online],
      globalFilter: false,
      defaultFilter: false,
      unregisteredFilter: false,
      tagFilter: null,
    });

    expect(filtered).to.deep.equal([online]);
    expect(filtered[0].status).to.equal('online');
  });
});
