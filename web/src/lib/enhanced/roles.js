import { USER_PERMISSIONS } from '@/lib/constants';

const enhancedMethods = {
  allowActions() {
    return this.can(USER_PERMISSIONS.manageProjectUsers);
  },
  getDeleteItemUrl(item) {
    return `${this.getSingleItemUrl()}?revision=${encodeURIComponent(item.revision)}`;
  },
  async loadItems() {
    if (!this.can(USER_PERMISSIONS.manageProjectUsers)) {
      this.items = [];
      return;
    }
    this.items = await this.loadEndpoint(this.getItemsUrl());
  },
};

export default enhancedMethods;
