<template xmlns:v-slot="http://www.w3.org/1999/XSL/Transform">
  <div v-if="items != null">
    <EditDialog
      v-model="editDialog"
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

    <v-dialog v-model="generatedKeyDialog" max-width="700">
      <v-card v-if="generatedKeyResult" class="generated-ssh-key-result">
        <v-card-title>SSH public key</v-card-title>
        <v-card-text>
          <v-alert dense text type="info">
            The private key is stored encrypted by Semaphore and is never displayed.
          </v-alert>
          <div>Algorithm: <code>{{ generatedKeyResult.algorithm }}</code></div>
          <div class="generated-ssh-key-fingerprint">
            Fingerprint: <code>{{ generatedKeyResult.fingerprint }}</code>
          </div>
          <div style="position: relative">
            <pre
              class="pa-2 mt-3 generated-ssh-key-public"
              style="overflow: auto; background: #616161; color: white; border-radius: 6px"
            >{{ generatedKeyResult.public_key }}</pre>
            <CopyClipboardButton
              style="position: absolute; right: 0; top: 0; transform: scale(0.9);"
              :text="generatedKeyResult.public_key"
            />
          </div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text color="primary" @click="generatedKeyDialog = false">Close</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="rotationDialog" max-width="640">
      <v-card v-if="rotationItem" class="generated-ssh-key-rotation">
        <v-card-title>Rotate SSH key</v-card-title>
        <v-card-text>
          <v-alert dense text type="warning">
            Update every authorized_keys destination with the new public key immediately.
            Running jobs keep their already loaded key.
          </v-alert>
          <ObjectRefsView
            object-title="access key"
            :object-refs="rotationRefs"
            :project-id="projectId"
            hide-warning
          />
          <v-select
            v-model="rotationAlgorithm"
            :items="generatedSSHKeyAlgorithms"
            item-value="id"
            item-text="name"
            label="Key algorithm"
            outlined
            dense
            :disabled="rotationLoading"
          />
          <v-alert v-if="rotationError" dense text type="error">{{ rotationError }}</v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text :disabled="rotationLoading" @click="rotationDialog = false">
            Cancel
          </v-btn>
          <v-btn
            color="primary"
            :loading="rotationLoading"
            :disabled="rotationLoading"
            @click="rotateSSHKey"
          >
            Rotate key
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <ObjectRefsDialog
      object-title="access key"
      :object-refs="itemRefs"
      :project-id="projectId"
      v-model="itemRefsDialog"
    />

    <YesNoDialog
      :title="$t('deleteKey')"
      :text="$t('askDeleteKey')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <v-toolbar flat >
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title>{{ $t('keyStore') }}</v-toolbar-title>
      <v-spacer></v-spacer>
      <v-btn
        color="primary"
        @click="editItem('new')"
        v-if="can(USER_PERMISSIONS.manageProjectResources)"
      >{{ $t('newKey') }}</v-btn>
    </v-toolbar>

    <KeyStoreMenu :project-id="projectId" />

    <v-data-table
      :headers="headers"
      :items="items"
      hide-default-footer
      class="mt-4"
      :items-per-page="Number.MAX_VALUE"
      style="max-width: calc(var(--breakpoint-xl) - var(--nav-drawer-width) - 200px); margin: auto;"
    >
      <template v-slot:item.name="{ item }">
        {{ item.name }}
        <v-chip
          color="error"
          v-if="item.empty && item.type !== 'none'"
          small
          style="font-weight: bold;"
          class="ml-2"
        >{{ $t('empty') }}</v-chip>
        <v-chip
          v-if="item.synchronized"
          x-small
          class="ml-2"
        >{{ $t('synchronized') }}</v-chip>
      </template>
      <template v-slot:item.type="{ item }">
        <code>{{ item.type }}</code>
      </template>
      <template v-slot:item.actions="{ item }">
        <v-btn-toggle dense :value-comparator="() => false">
          <v-btn @click="askDeleteItem(item.id)">
            <v-icon>mdi-delete</v-icon>
          </v-btn>
          <v-btn @click="editItem(item.id)">
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
          <v-btn v-if="canRotateSSHKey(item)" @click="beginSSHKeyRotation(item)">
            <v-icon>mdi-refresh</v-icon>
          </v-btn>
        </v-btn-toggle>
      </template>
    </v-data-table>

  </div>

</template>
<script>
import enhancedMethods from '@/lib/enhanced/keys';

import ItemListPageBase from '@/components/ItemListPageBase';
import KeyForm from '@/components/KeyForm.vue';
import PageMixin from '@/components/PageMixin';
import KeyStoreMenu from '@/components/KeyStoreMenu.vue';
import CopyClipboardButton from '@/components/CopyClipboardButton.vue';
import ObjectRefsView from '@/components/ObjectRefsView.vue';

export default {
  components: {
    KeyStoreMenu,
    KeyForm,
    CopyClipboardButton,
    ObjectRefsView,
  },

  mixins: [ItemListPageBase, PageMixin],

  props: {
    systemInfo: Object,
  },

  data() {
    return {
      generatedKeyDialog: false,
      generatedKeyResult: null,
      rotationDialog: false,
      rotationItem: null,
      rotationRefs: null,
      rotationAlgorithm: 'ed25519',
      rotationLoading: false,
      rotationError: null,
      generatedSSHKeyAlgorithms: [
        { id: 'ed25519', name: 'Ed25519 (recommended)' },
        { id: 'rsa-3072', name: 'RSA 3072 (compatibility)' },
      ],
    };
  },

  watch: {
    generatedKeyDialog(value) {
      if (!value) {
        this.generatedKeyResult = null;
      }
    },
    rotationDialog(value) {
      if (!value) {
        this.rotationItem = null;
        this.rotationRefs = null;
        this.rotationError = null;
      }
    },
  },

  methods: {
    ...enhancedMethods,

    getHeaders() {
      return [{
        text: this.$i18n.t('name'),
        value: 'name',
        width: '60%',
      },
      {
        text: this.$i18n.t('type'),
        value: 'type',
        width: '40%',
      },
      {
        value: 'actions',
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
<style scoped>
.generated-ssh-key-result,
.generated-ssh-key-rotation,
.generated-ssh-key-result ::v-deep .v-alert__content,
.generated-ssh-key-rotation ::v-deep .v-alert__content {
  min-width: 0;
  max-width: 100%;
}

.generated-ssh-key-fingerprint,
.generated-ssh-key-fingerprint code {
  overflow-wrap: anywhere;
  word-break: break-word;
}

.generated-ssh-key-public {
  box-sizing: border-box;
  max-width: 100%;
  min-width: 0;
  overflow-x: auto;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
