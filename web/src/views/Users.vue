<template>
  <div
    v-if="items != null"
    data-testid="users-page"
    :data-state="items.length === 0 ? 'empty' : 'ready'"
  >
    <EditDialog
      v-model="editDialog"
      test-id="userDialog"
      :save-button-text="itemId === 'new' ? $t('create') : $t('save')"
      :title="dialogTitle"
      :dont-close-on-save="itemId === 'new'"
      @save="loadItems()"
      :hide-buttons="hideEditDialogButtons"
    >
      <template v-slot:form="{ onSave, onError, needSave, needReset }">
        <UserForm
          :project-id="projectId"
          :item-id="itemId"
          @save="onSave($event); onUserSaved($event)"
          @error="onError"
          :need-save="needSave"
          :need-reset="needReset"
          :is-admin="isAdmin"
          :can-manage-global-roles="canManageGlobalRoles"
          @hide-action-buttons="hideEditDialogButtons = true"
          @show-action-buttons="hideEditDialogButtons = false"
          :auth-methods="authMethods"
          :login-with-password="(systemInfo || {}).login_with_password"
        />
      </template>
    </EditDialog>

    <YesNoDialog
      test-id="usersDeleteDialog"
      :title="$t('deleteUser')"
      :text="$t('userDeleteConsequences')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <v-dialog
      :model-value="Boolean(lifecycleTarget)"
      max-width="560"
      data-testid="users-lifecycleDialog"
      @update:model-value="closeLifecycleDialog"
    >
      <v-card v-if="lifecycleTarget">
        <v-card-title>
          {{ lifecycleTarget.disabled
            ? $t('userReactivateTitle', { name: lifecycleTarget.name })
            : $t('userDeactivateTitle', { name: lifecycleTarget.name }) }}
        </v-card-title>
        <v-card-text>
          {{ lifecycleTarget.disabled ? $t('userReactivateText') : $t('userDeactivateText') }}
          <v-alert
            v-if="lifecycleError"
            type="error"
            variant="outlined"
            density="compact"
            role="alert"
            class="mt-3"
            data-testid="users-lifecycleError"
          >{{ lifecycleError }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="closeLifecycleDialog(false)">{{ $t('cancel') }}</v-btn>
          <v-btn
            :color="lifecycleTarget.disabled ? 'primary' : 'error'"
            :loading="lifecycleSaving"
            data-testid="users-lifecycleConfirm"
            @click="changeLifecycle"
          >
            {{ lifecycleTarget.disabled ? $t('userReactivate') : $t('userDeactivate') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-toolbar flat>
      <v-btn
        v-if="!insideAdmin"
        icon
        variant="text"
        class="mr-4"
        @click="returnToProjects()"
        data-testid="users-back">
        <v-icon>mdi-arrow-left</v-icon>
      </v-btn>
      <v-toolbar-title data-testid="users-title">{{ $t('users') }}</v-toolbar-title>
      <v-spacer></v-spacer>
      <v-btn
        variant="elevated"
        v-if=" canManageUsers"
        color="primary"
        @click="editItem('new')"
        data-testid="users-new"
      >
        {{ $t('newUser') }}
      </v-btn>
    </v-toolbar>

    <v-divider />

    <v-alert v-if="!canManageUsers" variant="tonal" type="warning" class="PageAlert">
      You cannot manage global users.
    </v-alert>

    <v-data-table
      :headers="headers"
      :items="items"
      class="mt-4 FixedPageSizeTable UsersTable"
      :items-per-page-options="[20]"
      :items-per-page="20"
      data-testid="users-table"
      :data-state="items.length === 0 ? 'empty' : 'ready'"
    >
      <template v-slot:item.disabled="{ item }">
        <v-chip
          size="small"
          :color="item.disabled ? 'error' : 'success'"
          :data-testid="`users-rowStatus-${item.disabled ? 'deactivated' : 'active'}`"
        >
          {{ item.disabled ? $t('userStatusDeactivated') : $t('userStatusActive') }}
        </v-chip>
      </template>

      <template v-slot:item.external="{ item }">
        <v-icon
          v-if="item.external"
          data-testid="users-rowExternal-true"
        >mdi-checkbox-marked</v-icon>
        <v-icon v-else data-testid="users-rowExternal-false">mdi-checkbox-blank-outline</v-icon>
      </template>

      <template v-slot:item.alert="{ item }">
        <v-icon v-if="item.alert" data-testid="users-rowAlert-true">mdi-checkbox-marked</v-icon>
        <v-icon v-else data-testid="users-rowAlert-false">mdi-checkbox-blank-outline</v-icon>
      </template>

      <template v-slot:item.admin="{ item }">
        <v-icon v-if="item.admin" data-testid="users-rowAdmin-true">mdi-checkbox-marked</v-icon>
        <v-icon v-else data-testid="users-rowAdmin-false">mdi-checkbox-blank-outline</v-icon>
      </template>

      <template v-slot:item.actions="{ item }">
        <div v-if="canManageUsers" class="UsersTable__actions" data-testid="users-rowActions">
          <v-btn
            variant="text"
            size="small"
            prepend-icon="mdi-pencil"
            @click="editItem(item.id)"
            data-testid="users-rowEdit"
          >{{ $t('edit') }}</v-btn>

          <v-btn
            v-if="item.id !== userId"
            variant="text"
            size="small"
            :prepend-icon="item.disabled ? 'mdi-account-check' : 'mdi-account-cancel'"
            @click="openLifecycleDialog(item)"
            :data-testid="item.disabled ? 'users-rowReactivate' : 'users-rowDeactivate'"
          >{{ item.disabled ? $t('userReactivate') : $t('userDeactivate') }}</v-btn>

          <v-btn
            v-if="item.id !== userId"
            variant="text"
            size="small"
            color="error"
            prepend-icon="mdi-delete"
            class="UsersTable__delete"
            @click="askDeleteItem(item.id)"
            data-testid="users-rowDelete"
          >{{ $t('delete') }}</v-btn>
        </div>
      </template>
    </v-data-table>
  </div>
</template>
<style lang="scss">
// The tables of the Users screen (users and global roles, GlobalRolesList.vue) as Vuetify 2 drew
// them.
.UsersTable {
  // Rows as cards on a phone (`.v-data-table__mobile-row`): the name and the value pushed to the
  // two sides, the name 16 px from the value (Vuetify 4 splits the row in two equal columns, so
  // the chips of a role start in the middle).
  .v-data-table__tr--mobile > td {
    display: flex;
    justify-content: space-between;
    column-gap: 0;

    > .v-data-table__td-title {
      padding-inline-end: 16px;
    }
  }

  // An icon in a cell (the check boxes of Alert, Admin, External; in the value of a mobile row
  // too) is an icon of the theme (`.theme--light.v-icon`: .54, white in the dark theme), not the
  // text colour.
  > .v-table__wrapper > table > tbody > tr > td > .v-icon,
  > .v-table__wrapper > table > tbody > tr > td > .v-data-table__td-value > .v-icon {
    &.v-theme--light {
      color: rgba(0, 0, 0, .54);
    }

    &.v-theme--dark {
      color: #fff;
    }
  }
}

.UsersTable__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  white-space: nowrap;
}

// Delete sits apart from the frequent actions so a slip cannot hit it.
.UsersTable__delete {
  margin-left: 16px;
}
</style>
<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/users';

import axios from 'axios';
import EventBus from '@/event-bus';
import YesNoDialog from '@/components/YesNoDialog.vue';
import ItemListPageBase from '@/components/ItemListPageBase';
import EditDialog from '@/components/EditDialog.vue';
import UserForm from '@/components/UserForm.vue';

export default {
  inject: { insideAdmin: { default: false } },

  mixins: [ItemListPageBase],

  props: {
    authMethods: Object,
    systemInfo: Object,
    userId: Number,
  },

  components: {
    YesNoDialog,
    UserForm,
    EditDialog,
  },

  data() {
    return {
      hideEditDialogButtons: false,
      createdUserName: '',
      lifecycleTarget: null,
      lifecycleSaving: false,
      lifecycleError: '',
    };
  },

  computed: {
    ...enhancedComputed,

    dialogTitle() {
      if (this.itemId === 'new') return this.$t('newUser');
      if (this.createdUserName) return this.$t('userCreatedAssignRoles', { name: this.createdUserName });
      return this.$t('editUser');
    },

  },
  watch: {
    canManageUsers() {
      this.refreshHeaders();
    },
  },

  created() {
    this.refreshHeaders();
  },

  methods: {
    ...enhancedMethods,

    getHeaders() {
      return [
        {
          title: this.$t('name'),
          key: 'name',
          width: '50%',
        },
        {
          title: this.$t('username'),
          key: 'username',
        },
        {
          title: this.$t('email'),
          key: 'email',
        },
        {
          title: this.$t('userStatus'),
          key: 'disabled',
        },
        {
          title: this.$t('alert'),
          key: 'alert',
        },
        {
          title: this.$t('admin'),
          key: 'admin',
        },
        {
          title: this.$t('external'),
          key: 'external',
          width: '50%',
        },
        {
          title: this.$t('actions'),
          key: 'actions',
          sortable: false,
        },
      ];
    },

    editItem(itemId) {
      this.createdUserName = '';
      ItemListPageBase.methods.editItem.call(this, itemId);
    },

    openLifecycleDialog(user) {
      this.lifecycleError = '';
      this.lifecycleTarget = user;
    },

    closeLifecycleDialog(open) {
      if (!open) this.lifecycleTarget = null;
    },

    async changeLifecycle() {
      const target = this.lifecycleTarget;
      this.lifecycleSaving = true;
      this.lifecycleError = '';
      try {
        await axios.put(`/api/users/${target.id}/deactivation`, { disabled: !target.disabled });
        EventBus.$emit('i-snackbar', {
          color: 'success',
          text: target.disabled
            ? this.$t('userReactivated', { name: target.name })
            : this.$t('userDeactivated', { name: target.name }),
        });
        this.lifecycleTarget = null;
        await this.loadItems();
      } catch (err) {
        this.lifecycleError = err.response?.data?.error || err.message;
      } finally {
        this.lifecycleSaving = false;
      }
    },

    async returnToProjects() {
      EventBus.$emit('i-open-last-project');
    },

    getItemsUrl() {
      return '/api/users';
    },

    getSingleItemUrl() {
      return `/api/users/${this.itemId}`;
    },

    getEventName() {
      return 'i-user';
    },

  },
};
</script>
