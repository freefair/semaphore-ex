<template>
  <div v-if="items != null">
    <ObjectRefsDialog
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
      :title="$t('deleteStorage')"
      :text="$t('askDeleteStorage')"
      v-model="deleteItemDialog"
      @yes="deleteItem(itemId)"
    />

    <EditDialog
      v-model="editDialog"
      :save-button-text="itemId === 'new' ? $t('create') : $t('save')"
      :title="`${itemId === 'new' ? $t('nnew') : $t('edit')} ${dialogStorageType} Storage`"
      :max-width="450"
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
      <v-app-bar-nav-icon @click="showDrawer()"></v-app-bar-nav-icon>
      <v-toolbar-title>{{ $t('keyStore') }}</v-toolbar-title>
      <v-spacer></v-spacer>

      <v-menu offset-y>
        <template v-slot:activator="{ on, attrs }">
          <v-btn
            class="pr-2"
            v-bind="attrs"
            v-on="on"
            color="primary"
            v-if="can(USER_PERMISSIONS.manageProjectResources)"
            :disabled="!runtimeCanWrite"
            data-testid="secretStorage-newMenu"
          >
            New Storage
            <v-icon>mdi-chevron-down</v-icon>
          </v-btn>
        </template>
        <v-list>
          <v-list-item
            link
            @click="
              editItem('new');
              itemType = 'vault';
            "
            :disabled="!features.secret_storage_management || !runtimeCanWrite"
          >
            <v-list-item-icon>
              <v-icon>$vuetify.icons.hashicorp_vault</v-icon>
            </v-list-item-icon>
            <v-list-item-title>Hashicorp Vault</v-list-item-title>
          </v-list-item>

          <v-list-item
            link
            @click="
              editItem('new');
              itemType = 'openbao';
            "
            :disabled="!features.secret_storage_management || !runtimeCanWrite"
          >
            <v-list-item-icon>
              <v-icon>$vuetify.icons.openbao</v-icon>
            </v-list-item-icon>
            <v-list-item-title>OpenBao</v-list-item-title>
          </v-list-item>

          <div
            :class="{
              SecretStoragesEnterpriseMenu:
                features.secret_storage_management && !features.secret_storage_management_ex,
            }"
            :style="{
              backgroundColor:
                features.secret_storage_management && !features.secret_storage_management_ex
                  ? $vuetify.theme.dark
                    ? '#3f3f3f'
                    : '#f0f0f0'
                  : '',
            }"
          >
            <v-list-item
              link
              @click="
                editItem('new');
                itemType = 'aws_sm';
              "
              :disabled="!features.secret_storage_management_ex"
            >
              <v-list-item-icon>
                <v-icon>$vuetify.icons.aws_sm</v-icon>
              </v-list-item-icon>
              <v-list-item-title>AWS Secrets Manager</v-list-item-title>
            </v-list-item>

            <v-list-item
              link
              @click="
                editItem('new');
                itemType = 'azure_kv';
              "
              :disabled="!features.secret_storage_management_ex"
            >
              <v-list-item-icon>
                <v-icon>$vuetify.icons.azure_kv</v-icon>
              </v-list-item-icon>
              <v-list-item-title>Azure Key Vault</v-list-item-title>
            </v-list-item>

            <v-list-item
              link
              @click="
                editItem('new');
                itemType = 'dvls';
              "
              :disabled="!features.secret_storage_management_ex"
            >
              <v-list-item-icon>
                <v-icon>$vuetify.icons.dvls</v-icon>
              </v-list-item-icon>
              <v-list-item-title>Devolutions Server</v-list-item-title>
            </v-list-item>

            <a
              v-if="features.secret_storage_management && !features.secret_storage_management_ex"
              class="SecretStoragesEnterpriseMenu__overlay"
              href="https://semaphoreui.com/enterprise"
              target="_blank"
            >
              <div class="SecretStoragesEnterpriseMenu__button">
                Enterprise
                <v-icon color="white" small class="ml-1">mdi-arrow-right</v-icon>
              </div>
            </a>
          </div>
        </v-list>
      </v-menu>
    </v-toolbar>

    <v-tabs class="pl-4">
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
        v-if="isPro"
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
      text
      color="hsl(348deg, 86%, 61%)"
      class="PageAlert"
    >
      <span class="mr-1" v-html="$t('secret_storage_only_pro')"></span>

      <v-btn
        dark
        v-if="isAdmin"
        color="hsl(348deg, 86%, 61%)"
        @click="upgradeToPro('secret_storage_management')"
      >
        {{ $t('upgrade_to_pro') }}
      </v-btn>

      <span v-else style="font-weight: bold">
        {{ $t('contact_admin_to_upgrade') }}
      </span>
    </v-alert>

    <v-alert
      v-if="runtimeDecision && runtimeDecision.state !== 'active'"
      text
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
      :headers="headers"
      :items="items"
      hide-default-footer
      class="mt-4"
      :items-per-page="Number.MAX_VALUE"
      style="max-width: calc(var(--breakpoint-xl) - var(--nav-drawer-width) - 200px); margin: auto"
    >
      <template v-slot:item.name="{ item }">
        <v-icon class="mr-3" small>
          {{ getIcon(item.type) }}
        </v-icon>

        <span class="mr-2">{{ item.name }}</span>

        <v-chip v-if="item.readonly" style="transform: translateY(-1px)" color="info" small>
          Read only
        </v-chip>

        <div class="d-md-none text-caption text--secondary mt-1">
          Last attempt: {{ formatTimestamp(lastAttempt(item)) }}<br />
          Last success: {{ formatTimestamp(item.last_synced_at) }}
        </div>
      </template>

      <template v-slot:item.type="{ item }">
        <code>{{ item.type }}</code>
      </template>

      <template v-slot:item.last_attempt="{ item }">
        {{ formatTimestamp(lastAttempt(item)) }}
      </template>

      <template v-slot:item.last_success="{ item }">
        {{ formatTimestamp(item.last_synced_at) }}
      </template>

      <template v-slot:item.actions="{ item }">
        <v-btn-toggle dense :value-comparator="() => false" style="">
          <v-btn
            v-if="item.sync_direction === 'outbound'"
            @click="syncItem(item.id)"
            :disabled="
              !runtimeCanWrite || syncInProgress || !(item.sync_paths && item.sync_paths.length > 0)
            "
            aria-label="Synchronize selected keys"
          >
            <v-icon>mdi-sync</v-icon>
          </v-btn>
          <v-btn @click="openSyncHistory(item)" aria-label="Open synchronization history">
            <v-icon>mdi-history</v-icon>
          </v-btn>
          <v-btn
            @click="askDeleteItem(item.id)"
            :disabled="!runtimeCanWrite"
            aria-label="Delete secret storage"
          >
            <v-icon>mdi-delete</v-icon>
          </v-btn>
          <v-btn
            @click="editItem(item.id)"
            :disabled="!runtimeCanRead"
            aria-label="Edit secret storage"
          >
            <v-icon>mdi-pencil</v-icon>
          </v-btn>
        </v-btn-toggle>
      </template>
    </v-data-table>
  </div>
</template>

<style scoped lang="scss">
.SecretStoragesEnterpriseMenu {
  position: relative;
  cursor: not-allowed;
}

.SecretStoragesEnterpriseMenu__overlay {
  text-decoration: none !important;
  transition: 0.3s;
  z-index: 1;
  backdrop-filter: blur(5px);
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  left: 0;

  display: flex;
  justify-content: center;
  align-content: center;
  flex-wrap: wrap;
  opacity: 0;
  &:hover {
    opacity: 1;
  }
}

.SecretStoragesEnterpriseMenu__button {
  background: orange;
  color: white;
  font-weight: bold;
  border-radius: 100px;
  padding: 6px 16px;
}
</style>

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
    isPro: {
      type: Boolean,
      default: false,
    },
  },

  computed: {
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
          return '$vuetify.icons.hashicorp_vault';
        case 'openbao':
          return '$vuetify.icons.openbao';
        case 'dvls':
          return '$vuetify.icons.dvls';
        case 'aws_sm':
          return '$vuetify.icons.aws_sm';
        case 'azure_kv':
          return '$vuetify.icons.azure_kv';
        default:
          return '';
      }
    },

    getHeaders() {
      const headers = [
        {
          text: this.$i18n.t('name'),
          value: 'name',
          width: '40%',
        },
        {
          text: this.$i18n.t('type'),
          value: 'type',
          width: '20%',
        },
        {
          text: 'Last attempt',
          value: 'last_attempt',
          width: '20%',
        },
        {
          text: 'Last success',
          value: 'last_success',
          width: '20%',
        },
        {
          value: 'actions',
          sortable: false,
          width: '0%',
          align: 'end',
        },
      ];

      return this.$vuetify.breakpoint.smAndDown
        ? headers.filter((header) => ['name', 'actions'].includes(header.value))
        : headers;
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
