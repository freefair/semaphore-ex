<template>
  <div
    v-if="items != null"
    data-testid="secretStorages-page"
    :data-state="items.length === 0 ? 'empty' : 'ready'"
  >
    <ObjectRefsDialog
      test-id="secretStoragesRefsDialog"
      object-title="storage"
      :object-refs="itemRefs"
      :project-id="projectId"
      v-model="itemRefsDialog"
    />

    <SecretStorageSyncHistoryDialog
      ref="syncHistory"
      :project-id="projectId"
      :keys="localKeys"
      :can-resolve="runtimeCanWrite"
      :resolving="syncInProgress"
      @resolve="syncItem($event.storageId, $event.operationId)"
    />

    <YesNoDialog
      test-id="secretStoragesDeleteDialog"
      :title="$t('deleteStorage')"
      :text="$t('askDeleteStorage')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <EditDialog
      v-model="editDialog"
      test-id="secretStoragesDialog"
      :save-button-text="itemId === 'new' ? $t('create') : $t('save')"
      :title="`${itemId === 'new' ? $t('nnew') : $t('edit')} ${dialogStorageType} Storage`"
      :max-width="dialogMaxWidth"
      @save="loadItems()"
    >
      <template v-slot:form="{ onSave, onError, needSave, needReset }">
        <SecretStorageForm
          :project-id="projectId"
          :item-id="itemId"
          :item-type="itemType"
          @save="onSave"
          @error="onError"
          :need-save="needSave"
          :need-reset="needReset"
          :can-test-connection="runtimeCanExecute"
        />
      </template>
    </EditDialog>

    <v-toolbar flat>
      <v-app-bar-nav-icon
        @click="showDrawer()"
        data-testid="secretStorages-drawerToggle"
      ></v-app-bar-nav-icon>
      <v-toolbar-title class="flex-0-1" data-testid="secretStorages-title">
        {{ $t('keyStore') }}
      </v-toolbar-title>
      <v-spacer></v-spacer>

      <!-- The menu is wider than the button: Vuetify 2 pushed it against the 12 px margin of the
           window, Vuetify 4 lines it up with the end of the button, 4 px further left. -->
      <v-menu location="bottom" :offset="[0, 4]">
        <template v-slot:activator="{ props }">
          <v-btn
            class="pr-2"
            v-bind="props"
            color="primary"
            variant="elevated"
            v-if="can(USER_PERMISSIONS.manageProjectResources)"
            :disabled="!runtimeCanWrite"
            data-testid="secretStorage-newMenu"
          >
            New Storage
            <v-icon>mdi-chevron-down</v-icon>
          </v-btn>
        </template>
        <v-list class="SecretStoragesNewMenu" data-testid="secretStorages-newMenu">
          <v-list-item
            link
            @click="
              editItem('new');
              itemType = 'vault';
            "
            :disabled="!features.secret_storage_management || !runtimeCanWrite"
            data-testid="secretStorages-new-vault"
          >
            <template v-slot:prepend>
              <v-icon>$hashicorp_vault</v-icon>
            </template>
            <v-list-item-title>Hashicorp Vault</v-list-item-title>
          </v-list-item>

          <v-list-item
            link
            @click="
              editItem('new');
              itemType = 'openbao';
            "
            :disabled="!features.secret_storage_management || !runtimeCanWrite"
            data-testid="secretStorages-new-openbao"
          >
            <template v-slot:prepend>
              <v-icon>$openbao</v-icon>
            </template>
            <v-list-item-title>OpenBao</v-list-item-title>
          </v-list-item>

          <v-list-item
            link
            @click="
              editItem('new');
              itemType = 'infisical';
            "
            :disabled="!features.secret_storage_management || !runtimeCanWrite"
            data-testid="secretStorage-newInfisical"
          >
            <template #prepend>
              <v-icon>mdi-shield-key-outline</v-icon>
            </template>
            <v-list-item-title>{{ $t('infisicalPam') }}</v-list-item-title>
          </v-list-item>

          <template v-if="features.secret_storage_management_ex">
            <v-list-item
              link
              @click="
                editItem('new');
                itemType = 'aws_sm';
              "
              :disabled="!features.secret_storage_management_ex"
              data-testid="secretStorages-new-aws_sm"
            >
              <template v-slot:prepend>
                <v-icon>$aws_sm</v-icon>
              </template>
              <v-list-item-title>AWS Secrets Manager</v-list-item-title>
            </v-list-item>

            <v-list-item
              link
              @click="
                editItem('new');
                itemType = 'azure_kv';
              "
              :disabled="!features.secret_storage_management_ex"
              data-testid="secretStorages-new-azure_kv"
            >
              <template v-slot:prepend>
                <v-icon>$azure_kv</v-icon>
              </template>
              <v-list-item-title>Azure Key Vault</v-list-item-title>
            </v-list-item>

            <v-list-item
              link
              @click="
                editItem('new');
                itemType = 'dvls';
              "
              :disabled="!features.secret_storage_management_ex"
              data-testid="secretStorages-new-dvls"
            >
              <template v-slot:prepend>
                <v-icon>$dvls</v-icon>
              </template>
              <v-list-item-title>Devolutions Server</v-list-item-title>
            </v-list-item>
          </template>
        </v-list>
      </v-menu>
    </v-toolbar>

    <v-tabs class="pl-4" data-testid="secretStorages-tabs">
      <v-tab key="keys" :to="`/project/${projectId}/keys`" data-testid="keystore-keys">
        Keys
      </v-tab>

      <v-tab
        key="storages"
        :to="`/project/${projectId}/secret_storages`"
        data-testid="keystore-storages"
      >
        Storages
      </v-tab>

      <v-tab
        key="granted-credentials"
        :to="`/project/${projectId}/granted-credentials`"
        data-testid="keystore-granted-credentials"
      >
        Granted
      </v-tab>
    </v-tabs>

    <v-divider style="margin-top: -1px" />

    <v-alert
      v-if="!features.secret_storage_management"
      variant="tonal"
      color="hsl(348deg, 86%, 61%)"
      class="PageAlert"
      data-testid="secretStorages-proAlert"
    >
      Managed secret storage is not enabled.
    </v-alert>

    <v-alert
      v-if="runtimeDecision && runtimeDecision.state !== 'active'"
      variant="tonal"
      :type="runtimeDecision.state === 'read_only' ? 'info' : 'warning'"
      class="PageAlert"
      data-testid="secretStorage-runtimeCapability"
    >
      Runtime secrets are {{ formatCapabilityValue(runtimeDecision.state) }} ({{
        formatCapabilityValue(runtimeDecision.reason)
      }}). Existing configuration remains visible, but unavailable actions are blocked by the
      server.
    </v-alert>

    <v-data-table
      :headers="visibleHeaders"
      :items="items"
      hide-default-footer
      class="mt-4 SecretStoragesTable"
      :items-per-page="-1"
      data-testid="secretStorages-table"
      :data-state="items.length === 0 ? 'empty' : 'ready'"
    >
      <template v-slot:item.name="{ item }">
        <v-icon class="mr-3" size="small" :data-testid="`secretStorages-rowIcon-${item.type}`">
          {{ getIcon(item.type) }}
        </v-icon>

        <span class="mr-2" data-testid="secretStorages-rowName">{{ item.name }}</span>

        <v-chip
          v-if="item.readonly"
          class="SecretStoragesReadonly"
          style="transform: translateY(-1px)"
          color="info"
          size="small"
          data-testid="secretStorages-rowReadonly"
        >
          Read only
        </v-chip>

        <div class="d-md-none text-body-small text--secondary mt-1">
          Last attempt: {{ formatTimestamp(lastAttempt(item)) }}<br />
          Last success: {{ formatTimestamp(item.last_synced_at) }}
        </div>
      </template>

      <template v-slot:item.type="{ item }">
        <code data-testid="secretStorages-rowType">{{ item.type }}</code>
      </template>

      <template v-slot:item.last_attempt="{ item }">
        {{ formatTimestamp(lastAttempt(item)) }}
      </template>

      <template v-slot:item.last_success="{ item }">
        {{ formatTimestamp(item.last_synced_at) }}
      </template>

      <template v-slot:item.actions="{ item }">
        <v-btn-group
          density="compact"
          data-testid="secretStorages-rowActions"
        >
          <v-btn
            v-if="item.sync_direction === 'outbound'"
            @click="syncItem(item.id)"
            :disabled="
              !runtimeCanWrite || syncInProgress || !(item.sync_paths && item.sync_paths.length > 0)
            "
            aria-label="Synchronize selected keys"
            data-testid="secretStorages-rowSync"
          >
            <v-icon>mdi-sync</v-icon>
          </v-btn>
          <v-btn @click="openSyncHistory(item)" aria-label="Open synchronization history">
            <v-icon>mdi-history</v-icon>
          </v-btn>
          <v-btn @click="askDeleteItem(item.id)" data-testid="secretStorages-rowDelete"
            :disabled="!runtimeCanWrite"
            aria-label="Delete secret storage"
          >
            <v-icon>mdi-delete</v-icon>
          </v-btn>
          <v-btn @click="editItem(item.id)" data-testid="secretStorages-rowEdit"
            :disabled="!runtimeCanRead"
            aria-label="Edit secret storage"
          >
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
        </v-btn-group>
      </template>
    </v-data-table>
  </div>
</template>

<script>
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/secret-storages';

import axios from 'axios';
import ItemListPageBase from '@/components/ItemListPageBase';
import SecretStorageForm from '@/components/SecretStorageForm.vue';
import SecretStorageSyncHistoryDialog from '@/components/SecretStorageSyncHistoryDialog.vue';
import EventBus from '@/event-bus';
import { getErrorMessage } from '@/lib/error';

export default {
  components: { SecretStorageForm, SecretStorageSyncHistoryDialog },
  mixins: [ItemListPageBase],
  data() {
    return {
      itemType: 'vault',
      syncInProgress: false,
      localKeys: [],
    };
  },

  props: {
    systemInfo: Object,
  },

  computed: {
    visibleHeaders() {
      return this.$vuetify.display.smAndDown
        ? this.headers.filter((header) => ['name', 'actions'].includes(header.key))
        : this.headers;
    },
    ...enhancedComputed,
    features() {
      return this.systemInfo?.features || {};
    },

  },

  methods: {
    ...enhancedMethods,

    async syncItem(itemId, resolveOperationId = null) {
      this.syncInProgress = true;
      try {
        const response = await axios({
          method: 'post',
          url: `/api/project/${this.projectId}/secret_storages/${itemId}/sync`,
          data: {
            request_id: this.createSyncRequestID(),
            resolve_operation_id: resolveOperationId,
          },
          responseType: 'json',
        });
        EventBus.$emit('i-snackbar', {
          color: response.data.status === 'succeeded' ? 'success' : 'info',
          text: `Synchronization ${response.data.status}`,
        });
        await this.loadItems();
        await this.openSyncHistory(this.items.find((item) => item.id === itemId));
      } catch (err) {
        const operation = err.response?.data;
        if (operation?.id) {
          EventBus.$emit('i-snackbar', {
            color: operation.status === 'conflict' ? 'warning' : 'error',
            text:
              operation.status === 'conflict'
                ? 'Remote changes require an explicit overwrite decision'
                : `Synchronization failed: ${this.formatCapabilityValue(operation.error_category)}`,
          });
          await this.loadItems();
          await this.openSyncHistory(this.items.find((item) => item.id === itemId));
          return;
        }
        EventBus.$emit('i-snackbar', {
          color: 'error',
          text: getErrorMessage(err),
        });
      } finally {
        this.syncInProgress = false;
      }
    },

    getIcon(type) {
      switch (type) {
        case 'vault':
          return '$hashicorp_vault';
        case 'openbao':
          return '$openbao';
        case 'dvls':
          return '$dvls';
        case 'aws_sm':
          return '$aws_sm';
        case 'azure_kv':
          return '$azure_kv';
        case 'infisical':
          return 'mdi-shield-key-outline';
        default:
          return '';
      }
    },

    getHeaders() {
      return [
        {
          title: this.$t('name'),
          key: 'name',
          width: '40%',
        },
        {
          title: this.$t('type'),
          key: 'type',
          width: '20%',
        },
        {
          title: 'Last attempt',
          key: 'last_attempt',
          width: '20%',
        },
        {
          title: 'Last success',
          key: 'last_success',
          width: '20%',
        },
        {
          key: 'actions',
          sortable: false,
          width: '0%',
          align: 'end',
        },
      ];
    },
    getItemsUrl() {
      return `/api/project/${this.projectId}/secret_storages`;
    },
    getSingleItemUrl() {
      return `/api/project/${this.projectId}/secret_storages/${this.itemId}`;
    },
    getEventName() {
      return 'i-secret-storage';
    },
  },
};
</script>
