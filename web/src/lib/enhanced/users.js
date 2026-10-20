import { GLOBAL_PERMISSIONS } from '@/lib/constants';
import { hasGlobalPermission } from '@/lib/role-permissions';
import EventBus from '@/event-bus';

export const enhancedComputed = {
  canManageUsers() {
    return hasGlobalPermission(
      this.systemInfo || this.user,
      GLOBAL_PERMISSIONS.manageUsers,
      this.isAdmin,
    );
  },
  canManageGlobalRoles() {
    return hasGlobalPermission(
      this.systemInfo || this.user,
      GLOBAL_PERMISSIONS.manageRoles,
      this.isAdmin,
    );
  },

};

export const enhancedMethods = {
  allowActions() {
    return enhancedComputed.canManageUsers.call(this);
  },
  async onUserSaved(event) {
    if (event.action !== 'new' || !event.item || !event.item.id) return;
    this.itemId = event.item.id;
    EventBus.$emit('i-snackbar', { color: 'success', text: this.$i18n.t('userCreated') });
    await this.loadItems();
  },
  refreshHeaders() {
    this.headers = this.getHeaders().filter(
      (header) => this.canManageUsers || header.value !== 'actions',
    );
  },
  async loadItems() {
    if (!this.canManageUsers) {
      this.items = [];
      return;
    }
    this.items = await this.loadEndpoint(this.getItemsUrl());
  },
};
