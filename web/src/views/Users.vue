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
      :title="itemId === 'new' ? $t('newUser') : $t('editUser')"
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
      :text="$t('askDeleteUser')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <v-toolbar flat>
      <v-btn icon variant="text" class="mr-4" @click="returnToProjects()" data-testid="users-back">
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
        <div v-if="canManageUsers" style="white-space: nowrap" data-testid="users-rowActions">
          <v-btn
            icon
            variant="text"
            class="mr-1"
            @click="askDeleteItem(item.id)"
            data-testid="users-rowDelete"
          >
            <v-icon>mdi-delete</v-icon>
          </v-btn>

          <v-btn
            icon
            variant="text"
            class="mr-1"
            @click="editItem(item.id)"
            data-testid="users-rowEdit"
          >
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
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
</style>
<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/users';

import EventBus from '@/event-bus';
import YesNoDialog from '@/components/YesNoDialog.vue';
import ItemListPageBase from '@/components/ItemListPageBase';
import EditDialog from '@/components/EditDialog.vue';
import UserForm from '@/components/UserForm.vue';

export default {
  mixins: [ItemListPageBase],

  props: {
    authMethods: Object,
    systemInfo: Object,
  },

  components: {
    YesNoDialog,
    UserForm,
    EditDialog,
  },

  data() {
    return {
      hideEditDialogButtons: false,
    };
  },

  computed: {
    ...enhancedComputed,

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
