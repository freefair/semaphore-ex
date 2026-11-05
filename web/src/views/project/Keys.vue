<template>
  <div
    v-if="items != null"
    data-testid="keystore-page"
    :data-state="items.length === 0 ? 'empty' : 'ready'"
  >
    <EditDialog
      v-model="editDialog"
      test-id="keystoreDialog"
      :save-button-text="itemId === 'new' ? $t('create') : $t('save')"
      :title="`${itemId === 'new' ? $t('nnew') : $t('edit')} Key`"
      :max-width="450"
      @save="onKeySaved"
    >
      <template v-slot:form="{ onSave, onError, needSave, needReset }">
        <KeyForm
          :project-id="projectId"
          :item-id="itemId"
          @save="onSave"
          @error="onError"
          :need-save="needSave"
          :need-reset="needReset"
          :support-storages="features.secret_storages"
          :system-info="systemInfo"
        />
      </template>
    </EditDialog>

    <GeneratedSSHKeyResultDialog
      v-model="generatedKeyDialog"
      :generated-key-result="generatedKeyResult"
    />

            <RotateSSHKeyDialog
      v-model="rotationDialog"
              :rotation-item="rotationItem"
      :rotation-refs="rotationRefs"
      :project-id="projectId"
      v-model:rotation-algorithm="rotationAlgorithm"
      :algorithms="generatedSSHKeyAlgorithms"
      :rotation-loading="rotationLoading"
      :rotation-error="rotationError"
      @rotate="rotateSSHKey"
              test-id="keystorePublicKey-copy"
            />

    <ObjectRefsDialog
      test-id="keystoreRefsDialog"
      object-title="access key"
      :object-refs="itemRefs"
      :project-id="projectId"
      v-model="itemRefsDialog"
    />

    <YesNoDialog
      test-id="keystoreDeleteDialog"
      :title="$t('deleteKey')"
      :text="$t('askDeleteKey')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <v-toolbar flat>
      <v-app-bar-nav-icon
        @click="showDrawer()"
        data-testid="keystore-drawerToggle"
      ></v-app-bar-nav-icon>
      <v-toolbar-title class="flex-0-1" data-testid="keystore-title">
        {{ $t('keyStore') }}
      </v-toolbar-title>
      <v-spacer></v-spacer>
      <v-btn
        color="primary"
        variant="elevated"
        @click="editItem('new')"
        v-if="can(USER_PERMISSIONS.manageProjectResources)"
        data-testid="keystore-new"
      >{{ $t('newKey') }}</v-btn>
    </v-toolbar>

    <KeyStoreMenu :project-id="projectId" />

    <v-data-table
      :headers="headers"
      :items="items"
      hide-default-footer
      class="mt-4 KeysTable"
      :items-per-page="-1"
      data-testid="keystore-table"
      :data-state="items.length === 0 ? 'empty' : 'ready'"
    >
      <template v-slot:item.name="{ item }">
        {{ item.name }}
        <v-chip
          color="error"
          v-if="item.empty && item.type !== 'none'"
          size="small"
          style="font-weight: bold;"
          class="ml-2"
          data-testid="keystore-rowEmpty"
        >{{ $t('empty') }}</v-chip>
        <v-chip
          v-if="item.synchronized"
          size="x-small"
          class="ml-2"
          data-testid="keystore-rowSynchronized"
        >{{ $t('synchronized') }}</v-chip>
      </template>
      <template v-slot:item.type="{ item }">
        <code data-testid="keystore-rowType">{{ item.type }}</code>
      </template>
      <template v-slot:item.actions="{ item }">
        <v-btn-group
          density="compact"
          data-testid="keystore-rowActions"
        >
          <v-btn
            @click="askDeleteItem(item.id)"
            data-testid="keystore-rowDelete"
          >
            <v-icon>mdi-delete</v-icon>
          </v-btn>
          <v-btn
            @click="editItem(item.id)"
            data-testid="keystore-rowEdit"
          >
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
          <v-btn v-if="canRotateSSHKey(item)" @click="beginSSHKeyRotation(item)">
            <v-icon>mdi-refresh</v-icon>
          </v-btn>
        </v-btn-group>
      </template>
    </v-data-table>

  </div>

</template>
<style lang="scss">
// Not an inline style: the utility `mt-4` of Vuetify 4 has no `!important` and would lose to it.
.KeysTable {
  max-width: calc(var(--breakpoint-xl) - var(--nav-drawer-width) - 200px);
  margin: auto;
}

// The chips of the name column as high as in Vuetify 2 (`.v-size--small` 24 px,
// `.v-size--x-small` 16 px); Vuetify 4 makes them 26 and 20 px.
.KeysTable .v-chip.v-chip--size-small {
  --v-chip-height: 24px;
}

.KeysTable .v-chip.v-chip--size-x-small {
  --v-chip-height: 16px;
}
</style>
<script>
import { createEnhancedState, enhancedWatch } from '@/lib/enhanced/keys-state';

import RotateSSHKeyDialog from '@/components/enhanced/RotateSSHKeyDialog.vue';
import GeneratedSSHKeyResultDialog from '@/components/enhanced/GeneratedSSHKeyResultDialog.vue';
import enhancedMethods from '@/lib/enhanced/keys';

import ItemListPageBase from '@/components/ItemListPageBase';
import KeyForm from '@/components/KeyForm.vue';
import PageMixin from '@/components/PageMixin';
import KeyStoreMenu from '@/components/KeyStoreMenu.vue';

export default {
  components: {
    RotateSSHKeyDialog,
    GeneratedSSHKeyResultDialog,
    KeyStoreMenu,
    KeyForm,
  },

  mixins: [ItemListPageBase, PageMixin],

  props: {
    systemInfo: Object,
  },

  data() {
    return {
      ...createEnhancedState(),
    };
  },

  watch: {
    ...enhancedWatch,
  },

  methods: {
    ...enhancedMethods,

    getHeaders() {
      return [{
        title: this.$t('name'),
        key: 'name',
        width: '60%',
      },
      {
        title: this.$t('type'),
        key: 'type',
        width: '40%',
      },
      {
        key: 'actions',
        sortable: false,
        width: '0%',
      },
      ];
    },
    getItemsUrl() {
      return `/api/project/${this.projectId}/keys`;
    },
    getSingleItemUrl() {
      return `/api/project/${this.projectId}/keys/${this.itemId}`;
    },
    getEventName() {
      return 'i-keys';
    },
  },
};
</script>
