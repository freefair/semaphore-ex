import { expect } from 'chai';
import TemplateForm from '@/components/TemplateForm.vue';

const deferred = () => {
  let resolve;
  let reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const host = () => ({
  repositoryId: 1,
  item: { git_branch: 'main' },
  app: 'terraform',
  playbookDir: '',
  playbooks: null,
  playbooksAbort: null,
  playbooksLoading: false,
  ...TemplateForm.methods,
});

describe('Template repository picker request ownership', () => {
  it('retains the newer result after an older aborted request rejects', async () => {
    const vm = host(); const old = deferred(); const current = deferred();
    vm.loadProjectEndpoint = () => old.promise;
    const first = vm.loadPlaybooks();
    vm.loadProjectEndpoint = () => current.promise;
    const second = vm.loadPlaybooks();
    current.resolve(['current']); await second;
    old.reject(new Error('aborted')); await first;
    expect(vm.playbooks).to.deep.equal(['current']);
    expect(vm.playbooksLoading).to.equal(false);
  });
  it('ignores an old successful response after a replacement', async () => {
    const vm = host(); const old = deferred(); const current = deferred();
    vm.loadProjectEndpoint = () => old.promise;
    const first = vm.loadPlaybooks();
    vm.loadProjectEndpoint = () => current.promise;
    const second = vm.loadPlaybooks();
    current.resolve(['current']); await second;
    old.resolve(['stale']); await first;
    expect(vm.playbooks).to.deep.equal(['current']);
  });
  it('clears request ownership on repository change without accepting its late response', async () => {
    const vm = host(); const old = deferred();
    vm.loadProjectEndpoint = () => old.promise;
    const first = vm.loadPlaybooks();
    vm.repositoryId = 2; vm.playbookDir = 'prod/'; vm.loadBranches = async () => [];
    await TemplateForm.watch.repositoryId.call(vm);
    old.resolve(['previous repository']); await first;
    expect(vm.playbooks).to.equal(null);
    expect(vm.playbookDir).to.equal('');
    expect(vm.playbooksLoading).to.equal(false);
  });
  it('matches canonical suggestions for dot-prefixed and trailing-slash input', () => {
    const vm = host(); vm.fields = { playbook: { directories: true } };
    expect(vm.filterRepositoryPath('prod/eu', './prod/', 'prod/eu')).to.equal(true);
    expect(vm.filterRepositoryPath('prod', 'prod/', 'prod')).to.equal(true);
    expect(vm.filterRepositoryPath('dev', 'prod/', 'dev')).to.equal(false);
  });
});
