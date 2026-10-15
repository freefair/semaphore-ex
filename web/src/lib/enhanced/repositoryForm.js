import { isLocalRepositoryPath } from '@/lib/repositoryUrl';

function normalizeSharedCheckout() {
  if (this.item && isLocalRepositoryPath(this.item.git_url)) {
    this.item.monorepo = false;
  }
}

export const repositoryFormWatch = {
  'item.git_url': function normalizeLocalRepository() {
    normalizeSharedCheckout.call(this);
  },
};

export const repositoryFormMethods = {
  beforeSave() {
    normalizeSharedCheckout.call(this);
  },
};
