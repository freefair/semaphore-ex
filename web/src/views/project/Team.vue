<template>
  <div
    v-if="items != null && roles != null"
    data-testid="team-page"
    :data-state="items.length === 0 ? 'empty' : 'ready'"
  >
    <EditTeamMemberDialog
      v-model="editDialog"
      :project-id="projectId"
      :item-id="itemId"
      :invites-enabled="systemInfo.teams.invites_enabled"
      :invite-type="systemInfo.teams.invite_type"
      :roles="roles"
      @save="openInvites()"
    />

    <YesNoDialog
      test-id="teamDeleteDialog"
      :title="$t('deleteTeamMember')"
      :text="$t('askDeleteTMem')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <v-toolbar flat>
      <v-app-bar-nav-icon
        @click="showDrawer()"
        data-testid="team-drawerToggle"
      ></v-app-bar-nav-icon>
      <v-toolbar-title class="flex-0-1" data-testid="team-title">{{ $t('team2') }}</v-toolbar-title>
      <v-spacer></v-spacer>
      <v-btn
        v-if="systemInfo.teams.members_can_leave"
        color="error"
        variant="elevated"
        @click="leftProject()"
        class="mr-2"
        data-testid="team-leave"
        >{{ $t('LeaveProject') }}
      </v-btn>
      <v-btn
        color="primary"
        variant="elevated"
        @click="editItem('new')"
        v-if="can(USER_PERMISSIONS.manageProjectUsers)"
        data-testid="team-new"
        >{{ $t('newTeamMember') }}
      </v-btn>
    </v-toolbar>

    <TeamMenu
      :project-id="projectId"
      :system-info="systemInfo"
      :can-manage-roles="can(USER_PERMISSIONS.manageProjectUsers)"
    />

    <v-divider style="margin-top: -1px" />

    <v-data-table
      data-testid="team-table"
      :data-state="pendingSaves > 0 ? 'loading' : (items.length === 0 ? 'empty' : 'ready')"
      :headers="headers"
      :items="items"
      hide-default-footer
      class="mt-4 TeamTable"
      :items-per-page="-1"
    >
      <template v-slot:item.roles="{ item }">
        <div class="d-flex align-center TeamTable__roles" data-testid="team-rowRoles">
          <v-tooltip v-for="mapped in item.mapped_roles || []"
                     :key="`${mapped.provider}/${mapped.group}/${mapped.role}`" location="bottom">
            <template v-slot:activator="{ props }">
              <v-chip
                size="small"
                variant="outlined"
                class="mr-1"
                v-bind="props"
                :data-testid="`team-rowMappedRole-${mapped.role}`"
              >
                <v-icon size="x-small" start>mdi-lock</v-icon>
                {{ (roles.find((r) => r.slug === mapped.role) || { name: mapped.role }).name }}
              </v-chip>
            </template>
            {{ mapped.provider }}: {{ mapped.group }}
          </v-tooltip>
          <v-autocomplete
            v-if="can(USER_PERMISSIONS.manageProjectUsers)
              && !isDirectoryManaged(item)"
            v-model="item.roles"
            :items="roleItems(item)"
            item-value="slug"
            item-title="name"
            multiple
            chips
            closable-chips
            hide-details
            variant="underlined"
            density="compact"
            class="pt-0 mt-0"
            :menu-props="{ contentClass: 'TeamRoleMenu TeamAutocompleteMenu', maxHeight: 304 }"
            @update:model-value="updateProjectUser(item)"
            data-testid="team-rowRolesSelect"
          >
            <template v-slot:item="{ props, item: role }">
              <v-list-item
                v-bind="props"
                :title="role.name"
                :subtitle="role.slug"
                :data-testid="`team-roleOption-${role.slug}`"
              />
            </template>
          </v-autocomplete>
          <div v-else data-testid="team-rowRoleChips">
            <v-chip v-for="role in roleItems(item).filter((r) => item.roles.includes(r.slug))"
                    :key="role.slug" size="small" class="mr-1"
                    :data-testid="`team-rowRole-${role.slug}`">{{ role.name }}</v-chip>
          </div>
        </div>
        <div
          v-if="rowStatus[item.id]"
          :class="`text-${rowStatus[item.id].type} text-body-small mt-1`"
          role="status"
          :data-testid="`team-rowStatus-${item.id}`"
        >{{ rowStatus[item.id].text }}</div>
        <PermissionSummary
          class="mt-1"
          :permission-ids="memberPermissionIds(item)"
          :catalog="permissionCatalog"
        />
        <v-btn
          v-if="canInspectPermissions(item)"
          variant="text"
          size="small"
          class="px-0 mt-1"
          prepend-icon="mdi-shield-search"
          @click="showEffectivePermissions(item)"
          :data-testid="`team-effectivePermissions-${item.id}`"
        >{{ $t('permEffectiveShow') }}</v-btn>
      </template>

      <template v-slot:item.actions="{ item }">
        <v-btn-group
          density="compact"
          data-testid="team-rowActions"
        >
          <v-btn
            v-if="can(USER_PERMISSIONS.manageProjectUsers) && !isDirectoryManaged(item)"
            @click="askDeleteItem(item.id)"

            data-testid="team-rowDelete"
          >
            <v-icon>mdi-delete</v-icon>
          </v-btn>
        </v-btn-group>
      </template>
    </v-data-table>

    <v-dialog
      :model-value="effective != null"
      @update:model-value="(open) => { if (!open) effective = null; }"
      max-width="720"
      scrollable
    >
      <v-card v-if="effective" data-testid="team-effectivePermissionsDialog">
        <v-card-title>
          {{ $t('permEffectiveTitle') }}: {{ effective.member.name || effective.member.username }}
        </v-card-title>
        <v-card-text>
          <v-progress-linear v-if="effective.loading" indeterminate />
          <v-alert
            v-else-if="effective.error"
            type="error"
            variant="tonal"
            data-testid="team-effectivePermissionsError"
          >{{ effective.error }} {{ effective.detail }}</v-alert>
          <template v-else>
            <PermissionSummary
              :permission-ids="effective.permissions"
              :catalog="permissionCatalog"
            />
            <v-table density="compact" class="mt-4" data-testid="team-effectivePermissionsSources">
              <thead>
                <tr>
                  <th>{{ $t('permissions') }}</th>
                  <th>{{ $t('permEffectiveGrantedBy') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="permissionId in effective.permissions" :key="permissionId">
                  <td>{{ permissionDescription(permissionId) }}</td>
                  <td>{{ effectiveSourcesFor(permissionId).join(', ') }}</td>
                </tr>
              </tbody>
            </v-table>
          </template>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="effective = null">{{ $t('close') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>
<style lang="scss">
// Not an inline style: the utility `mt-4` of Vuetify 4 has no `!important` and would lose to it.
.TeamTable {
  max-width: calc(var(--breakpoint-xl) - var(--nav-drawer-width) - 200px);
  margin: auto;

  // The rows clip what runs past the table bottom, as `.v-data-table__wrapper` did
  // (`overflow-y: hidden`): the line of a two-chip field in the last row stays out of sight.
  > .v-table__wrapper {
    overflow-y: hidden;
  }

  // Mobile rows: a flex row of Vuetify 2 (`.v-data-table__mobile-row`: space-between, the header
  // 16 px from the value), not the two grid columns of Vuetify 4; the role field takes the rest
  // of the row.
  .v-data-table__tr--mobile > td {
    display: flex;
    justify-content: space-between;
    column-gap: 0;

    > .v-data-table__td-title {
      padding-right: 16px;
    }

    // The role field fills the rest of the row: the hidden input of a Vuetify 2 autocomplete
    // was as wide as a text field, so the field always reached the row end.
    > .v-data-table__td-value:has(> .TeamTable__roles > .v-autocomplete) {
      flex: 1 1 auto;
      min-width: 0;
    }
  }
}

// The chips of a row as in Vuetify 2: small ones 24 px high (`.v-size--small`; Vuetify 4: 26 px),
// the frame of an outlined one (a role from a group mapping) in the light border colour
// (`.v-chip` border-color: .12), not in the text colour.
.TeamTable__roles .v-chip {
  &.v-chip--size-small {
    --v-chip-height: 24px;
  }

  &.v-chip--variant-outlined {
    border-color: rgba(var(--v-theme-on-surface), var(--v-border-opacity));
  }
}

// The role field of a row: a dense autocomplete with small chips of Vuetify 2
// (`.v-select--chips--small.v-input--dense`): a 26 px box, chips 4 px from its edge and from each
// other, stacked without a gap, the arrow at its top, the line 1 px under the box
// (`.v-input__slot:before`, `bottom: -1px`). The field and its chips keep their content width,
// as in Vuetify 2, so the role column is as wide as it was (Vuetify 4 lets a field shrink to 0).
.TeamTable .v-autocomplete {
  grid-template-columns: max-content minmax(min-content, 1fr) max-content;

  > .v-input__control > .v-field {
    grid-template-columns: min-content minmax(min-content, 1fr) min-content min-content;
  }

  .v-field__input {
    min-width: auto;
    min-height: 26px;
    padding: 0;
    align-items: center;
    gap: 0;
    // The chips in full colour (`.v-select__selections` set the colour, not an opacity).
    opacity: 1;
  }

  .v-autocomplete__selection {
    max-width: none;
    margin: 0 4px;

    > .v-chip {
      max-width: none;
    }
  }

  .v-field__append-inner {
    padding-top: 0;
    align-items: flex-start;
  }

  .v-field__outline::before,
  .v-field__outline::after {
    height: calc(100% + 1px);
  }
}
</style>
<script>
import enhancedMethods from '@/lib/enhanced/team';

import ItemListPageBase from '@/components/ItemListPageBase';
import EditTeamMemberDialog from '@/components/EditTeamMemberDialog.vue';
import axios from 'axios';
import { USER_PERMISSIONS } from '@/lib/constants';
import groupRolesForSelect from '@/lib/roles';
import TeamMenu from '@/components/TeamMenu.vue';
import PermissionSummary from '@/components/enhanced/PermissionSummary.vue';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

function isDirectoryManaged(member) {
  return Array.isArray(member?.mapped_roles) && member.mapped_roles.length > 0;
}

export default {
  components: { TeamMenu, EditTeamMemberDialog, PermissionSummary },
  mixins: [ItemListPageBase],

  props: {
    systemInfo: Object,
    userId: Number,
  },

  data() {
    return {
      roles: null,
      rowStatus: {},
      permissionCatalog: [],
      effective: null,
      saving: Promise.resolve(),
      pendingSaves: 0,
    };
  },

  async created() {
    this.loadPermissionCatalog();
    this.roles = (
      await axios({
        method: 'get',
        url: `/api/project/${this.projectId}/roles/all`,
        responseType: 'json',
      })
    ).data;
  },

  methods: {
    ...enhancedMethods,

    openInvites() {
      if (this.systemInfo.teams.invites_enabled) {
        this.$router.push(`/project/${this.projectId}/invites`);
        return;
      }
      this.loadItems();
    },

    async leftProject() {
      try {
        await axios({
          method: 'delete',
          url: `/api/project/${this.projectId}/me`,
          responseType: 'json',
        });
        window.location.reload();
      } catch (err) {
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
      }
    },

    roleItems(member) {
      // Keep assigned roles visible when their definitions are unavailable.
      const missing = member.roles
        .filter((slug) => !this.roles.some((role) => role.slug === slug))
        .map((slug) => ({ slug, name: slug }));
      return [...groupRolesForSelect(this.roles, (key) => this.$t(key)), ...missing];
    },

    isDirectoryManaged(member) {
      return isDirectoryManaged(member);
    },

    async updateProjectUser(user) {
      if (isDirectoryManaged(user)) {
        EventBus.$emit('i-snackbar', { color: 'info', text: this.$t('memberKeepsMappedRoles') });
        return;
      }
      const roles = [...user.roles];
      const save = async () => {
        // Removing a member is the delete button, the server refuses an empty role list.
        if (roles.length > 0) {
          try {
            await axios({
              method: 'put',
              url: `/api/project/${this.projectId}/users/${user.id}`,
              responseType: 'json',
              data: { roles, revision: user.revision },
            });
            Object.assign(user, { revision: user.revision + 1 });
            this.setRowStatus(user.id, 'success', this.$t('teamRolesSaved'));
          } catch (err) {
            this.setRowStatus(user.id, 'error', err.response?.data?.message || getErrorMessage(err));
          }
        } else {
          this.setRowStatus(user.id, 'error', this.$t('memberNeedsRole'));
        }
        this.pendingSaves -= 1;
        // A reload before the next queued save would drop the pick it is saving.
        if (this.pendingSaves === 0) {
          await this.loadItems();
        }
      };
      // Saves go one at a time, so an older role list is never written after a newer one.
      this.pendingSaves += 1;
      this.saving = this.saving.then(save, save);
      await this.saving;
    },

    async deleteItem(itemId) {
      const member = this.items?.find((candidate) => candidate.id === itemId);
      if (isDirectoryManaged(member)) {
        EventBus.$emit('i-snackbar', { color: 'info', text: this.$t('memberKeepsMappedRoles') });
        return;
      }
      this.itemId = itemId;
      try {
        await axios({
          method: 'delete',
          url: this.getSingleItemUrl(),
          responseType: 'json',
        });
      } catch (err) {
        EventBus.$emit('i-snackbar', { color: 'error', text: getErrorMessage(err) });
        return;
      }
      await this.loadItems();
      if (this.items.some((candidate) => candidate.id === itemId)) {
        EventBus.$emit('i-snackbar', { color: 'info', text: this.$t('memberKeepsMappedRoles') });
      }
    },

    allowActions() {
      return this.can(USER_PERMISSIONS.manageProjectUsers);
    },

    getHeaders() {
      return [
        {
          title: this.$t('name'),
          key: 'name',
          width: '40%',
        },
        {
          title: this.$t('username'),
          key: 'username',
          width: '30%',
        },
        {
          title: this.$t('role'),
          key: 'roles',
          sortable: false,
          width: '30%',
        },
        {
          key: 'actions',
          sortable: false,
          width: '0%',
        },
      ];
    },

    getSingleItemUrl() {
      return `/api/project/${this.projectId}/users/${this.itemId}`;
    },
    getItemsUrl() {
      return `/api/project/${this.projectId}/users?sort=name&order=asc`;
    },
    getEventName() {
      return 'i-repositories';
    },
  },
};
</script>
