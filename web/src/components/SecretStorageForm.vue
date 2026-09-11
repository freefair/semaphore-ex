<template>
  <v-form ref="form" lazy-validation v-model="formValid" v-if="item != null">
    <v-alert :value="formError" type="error" class="mb-6" dismissible>{{ formError }} </v-alert>

    <v-text-field
      v-model="item.name"
      :label="$t('name')"
      :rules="[(v) => !!v || $t('name_required')]"
      required
      :disabled="formSaving"
      outlined
      dense
    ></v-text-field>

    <v-text-field
      v-if="item.type !== 'aws_sm' && item.type !== 'azure_kv'"
      v-model="item.params.url"
      :label="$t('Server URL')"
      :disabled="formSaving"
      :rules="[(v) => !!v || $t('url_required')]"
      required
      data-testid="secretStorage-vaultURL"
      outlined
      dense
    ></v-text-field>

    <div v-if="item.type === 'vault' || item.type === 'openbao'">
      <v-alert text type="info" dense data-testid="secretStorage-runtimeNotice">
        {{ runtimeProviderNotice }} Secret values are never listed or returned by the API.
      </v-alert>

      <v-select
        v-model="item.sync_direction"
        label="Synchronization direction"
        :items="runtimeSyncDirections"
        item-value="value"
        item-text="text"
        :disabled="formSaving"
        data-testid="secretStorage-syncDirection"
        outlined
        dense
      />

      <v-alert v-if="isManagedOutbound" text type="warning" dense>
        Outbound synchronization writes only the selected Semaphore keys below. Existing remote
        values cause a conflict and require an explicit overwrite decision.
      </v-alert>

      <v-text-field
        v-model="item.params.mount"
        :label="$t('Mount')"
        hint="KV v2 mount; 'secret' by default"
        :disabled="formSaving"
        data-testid="secretStorage-vaultMount"
        outlined
        dense
      ></v-text-field>

      <v-text-field
        v-model="item.params.namespace"
        :label="$t('Namespace')"
        :hint="
          item.type === 'openbao'
            ? 'OpenBao namespaces (v2.3+)'
            : 'For Vault Enterprise and HCP Dedicated only'
        "
        :disabled="formSaving"
        data-testid="secretStorage-vaultNamespace"
        outlined
        dense
      ></v-text-field>

      <RuntimeProviderAuthFields
        :ca-certificate.sync="item.params.ca_certificate"
        :timeout.sync="item.params.timeout"
        :auth-method.sync="item.params.auth_method"
        :auth-mount.sync="item.params.auth_mount"
        :role-id.sync="item.params.role_id"
        :role.sync="item.params.role"
        :secret-storage.sync="secretStorage"
        :runtime-credential-label="runtimeCredentialLabel"
        :runtime-auth-methods="runtimeAuthMethods"
        :form-saving="formSaving"
      />

      <v-text-field
        v-if="secretStorage === 'database'"
        class="masked-secret-input"
        v-model="item.secret"
        :label="runtimeCredentialLabel"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('token_required')]"
        required
        data-testid="secretStorage-vaultToken"
        outlined
        dense
        append-icon="mdi-lock"
      ></v-text-field>

      <v-text-field
        v-else
        v-model="item.secret"
        :label="secretStorage === 'env' ? $t('Env var name') : $t('Path to the file')"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('envvar_required')]"
        required
        data-testid="secretStorage-vaultTokenSource"
        outlined
        dense
      ></v-text-field>

      <div v-if="itemId !== 'new'" class="mb-5">
        <v-btn
          outlined
          color="primary"
          :loading="connectionTesting"
          :disabled="formSaving || !canTestConnection"
          data-testid="secretStorage-testConnection"
          @click="testConnection"
        >
          Test connection
        </v-btn>
        <v-alert
          v-if="connectionHealth"
          class="mt-3 mb-0"
          dense
          text
          :type="connectionHealth.state === 'healthy' ? 'success' : 'error'"
          data-testid="secretStorage-connectionHealth"
        >
          {{ connectionHealthMessage }}
        </v-alert>
      </div>
    </div>

    <div v-else-if="item.type === 'dvls'">
      <v-checkbox
        class="pt-0 mb-2"
        style="margin-top: -5px"
        v-model="item.params.insecure_tls"
        label="Skip TLS certificate verification (insecure)"
        :disabled="formSaving"
      />

      <v-text-field
        v-model="item.params.vault_id"
        :label="$t('Vault ID')"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('key_required')]"
        required
        data-testid="secretStorage-dvlsKey"
        outlined
        dense
      ></v-text-field>

      <v-text-field
        v-model="item.params.app_key"
        :label="$t('App Key')"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('key_required')]"
        required
        data-testid="secretStorage-dvlsKey"
        outlined
        dense
      ></v-text-field>

      <SecretSourceToggle v-model="secretStorage" label="App secret" :disabled="formSaving" />

      <v-text-field
        v-if="secretStorage === 'database'"
        class="TextInput TextInput--no-legend masked-secret-input"
        v-model="item.secret"
        :label="$t('Secret')"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('secret_required')]"
        required
        data-testid="secretStorage-dvlsSecret"
        outlined
        dense
        append-icon="mdi-lock"
      ></v-text-field>

      <v-text-field
        v-else
        class="TextInput TextInput--no-legend"
        v-model="item.secret"
        :label="secretStorage === 'env' ? $t('Env var name') : $t('Path to the file')"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('envvar_required')]"
        required
        data-testid="secretStorage-dvlsEnv"
        outlined
        dense
      ></v-text-field>
    </div>

    <div v-else-if="item.type === 'aws_sm'">
      <v-text-field
        v-model="item.params.region"
        label="Region"
        :disabled="formSaving"
        :rules="[(v) => !!v || 'Region is required']"
        required
        placeholder="us-east-1"
        data-testid="secretStorage-awsRegion"
        outlined
        dense
      ></v-text-field>

      <v-text-field
        v-model="item.params.endpoint_url"
        label="Endpoint URL (optional)"
        :disabled="formSaving"
        hint="Leave empty to use the default AWS endpoint"
        data-testid="secretStorage-awsEndpointURL"
        outlined
        dense
      ></v-text-field>

      <v-checkbox
        class="pt-0 mb-2"
        style="margin-top: -5px"
        v-model="item.params.use_iam_role"
        label="Use IAM Role / Instance Profile"
        :disabled="formSaving"
        data-testid="secretStorage-awsUseIamRole"
      />

      <div :class="{ 'aws-credentials--inactive': useIamRole }">
        <v-text-field
          v-model="item.params.access_key_id"
          label="Access Key ID"
          :disabled="formSaving || useIamRole"
          :rules="[(v) => !!v || useIamRole || 'Access Key ID is required']"
          required
          data-testid="secretStorage-awsAccessKeyId"
          outlined
          dense
        ></v-text-field>

        <SecretSourceToggle
          v-model="secretStorage"
          label="Secret Key"
          :disabled="formSaving || useIamRole"
        />

        <v-text-field
          v-if="secretStorage === 'database'"
          class="TextInput TextInput--no-legend masked-secret-input"
          v-model="item.secret"
          label="Secret Access Key"
          :disabled="formSaving || useIamRole"
          :rules="[(v) => !!v || !awsSecretRequired || 'Secret Access Key is required']"
          required
          data-testid="secretStorage-awsSecretKey"
          outlined
          dense
          append-icon="mdi-lock"
        ></v-text-field>

        <v-text-field
          v-else
          class="TextInput TextInput--no-legend"
          v-model="item.secret"
          :label="secretStorage === 'env' ? $t('Env var name') : $t('Path to the file')"
          :disabled="formSaving || useIamRole"
          :rules="[(v) => !!v || !awsSecretRequired || $t('envvar_required')]"
          required
          data-testid="secretStorage-awsSecretKeySource"
          outlined
          dense
        ></v-text-field>
      </div>
    </div>

    <div v-else-if="item.type === 'azure_kv'">
      <v-text-field
        v-model="item.params.vault_url"
        label="Vault URL"
        :disabled="formSaving"
        :rules="[(v) => !!v || 'Vault URL is required']"
        required
        placeholder="https://my-vault.vault.azure.net"
        data-testid="secretStorage-azureVaultURL"
        outlined
        dense
      ></v-text-field>

      <v-text-field
        v-model="item.params.tenant_id"
        label="Tenant ID"
        :disabled="formSaving"
        :rules="[(v) => !!v || 'Tenant ID is required']"
        required
        data-testid="secretStorage-azureTenantId"
        outlined
        dense
      ></v-text-field>

      <v-text-field
        v-model="item.params.client_id"
        label="Client ID"
        :disabled="formSaving"
        :rules="[(v) => !!v || 'Client ID is required']"
        required
        data-testid="secretStorage-azureClientId"
        outlined
        dense
      ></v-text-field>

      <SecretSourceToggle v-model="secretStorage" label="Client Secret" :disabled="formSaving" />

      <v-text-field
        v-if="secretStorage === 'database'"
        class="TextInput TextInput--no-legend masked-secret-input"
        v-model="item.secret"
        label="Client Secret"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || 'Client Secret is required']"
        required
        data-testid="secretStorage-azureClientSecret"
        outlined
        dense
        append-icon="mdi-lock"
      ></v-text-field>

      <v-text-field
        v-else
        class="TextInput TextInput--no-legend"
        v-model="item.secret"
        :label="secretStorage === 'env' ? $t('Env var name') : $t('Path to the file')"
        :disabled="formSaving"
        :rules="[(v) => !!v || itemId !== 'new' || $t('envvar_required')]"
        required
        data-testid="secretStorage-azureClientSecretSource"
        outlined
        dense
      ></v-text-field>
    </div>

    <v-checkbox
      v-if="!isRuntimeProvider"
      v-model="item.readonly"
      :label="$t('Read only')"
      :disabled="formSaving"
      hide-details
      style="position: absolute; bottom: 15px; margin: 0; left: 25px"
    />

    <v-alert v-else text dense type="info" class="mb-3">
      {{
        isManagedOutbound
          ? 'Managed outbound writes are enabled for selected keys.'
          : 'Read-only runtime resolution is enabled. Remote data cannot be changed.'
      }}
    </v-alert>

    <div class="d-flex items-center justify-space-between">
      <v-checkbox
        class="mt-0"
        v-model="item.sync_enabled"
        :label="isRuntimeProvider ? 'Automatic synchronization enabled' : $t('Sync keys enabled')"
        :disabled="formSaving"
        v-if="!isRuntimeProvider || isManagedOutbound"
      />

      <v-btn
        style="margin-right: -10px"
        text
        color="primary"
        @click="syncSettingsDialog = true"
        :disabled="formSaving"
        v-if="item.sync_enabled || isManagedOutbound"
      >
        <v-icon left>mdi-cog-sync</v-icon>
        Sync paths
        <v-chip class="ml-2" outlined style="transform: translateY(-1px)" color="primary" small>
          {{ item.sync_paths.length }}</v-chip
        >
      </v-btn>
    </div>

    <v-dialog v-model="syncSettingsDialog" :max-width="isManagedOutbound ? 760 : 500" persistent>
      <v-card>
        <v-card-title>Sync paths</v-card-title>
        <v-card-text class="pt-4 pb-0">
          <v-text-field
            style="width: 140px"
            v-if="item.sync_enabled"
            v-model.number="item.sync_interval"
            min="0"
            :label="$t('Auto-sync interval')"
            persistent-hint
            :disabled="formSaving"
            suffix="minutes"
            outlined
            dense
          ></v-text-field>

          <SecretStorageSyncOptionsForm
            v-model="item.sync_paths"
            :managed="isRuntimeProvider"
            :keys="managedLocalKeys"
            :default-mount="item.params.mount || 'secret'"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn text color="blue darken-1" @click="syncSettingsDialog = false">
            {{ $t('close') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-form>
</template>
<script>
import { createEnhancedState, enhancedWatch } from '@/lib/enhanced/secret-storage-form-state';

import RuntimeProviderAuthFields from '@/components/enhanced/RuntimeProviderAuthFields.vue';
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/secret-storage-form';

import ItemFormBase from '@/components/ItemFormBase';
import SecretStorageSyncOptionsForm from '@/components/SecretStorageSyncOptionsForm.vue';
import SecretSourceToggle from '@/components/SecretSourceToggle.vue';

export default {
  components: { RuntimeProviderAuthFields, SecretStorageSyncOptionsForm, SecretSourceToggle },

  props: {
    itemType: String,
    canTestConnection: {
      type: Boolean,
      default: true,
    },
  },

  mixins: [ItemFormBase],

  data() {
    return {
      secretStorage: 'database',
      secretStorageReady: false,
      syncSettingsDialog: false,
      // IAM role state of the storage at load time.
      initialUseIamRole: false,
      ...createEnhancedState(),
    };
  },

  computed: {
    ...enhancedComputed,

    useIamRole() {
      return this.item?.params?.use_iam_role;
    },

    awsSecretRequired() {
      if (this.useIamRole) {
        return false;
      }

      // Switching the IAM role off removes the previously stored credentials,
      // so a new secret must be provided.
      return this.itemId === 'new' || this.initialUseIamRole;
    },
  },

  methods: {
    ...enhancedMethods,
    getNewItem() {
      return {
        sync_enabled: false,
        sync_interval: 0,
        sync_paths: [],
        params: {},
      };
    },

    async afterLoadData() {
      if (!this.item.params) {
        this.item.params = {};
      }

      if (!this.item.sync_paths) {
        this.$set(this.item, 'sync_paths', []);
      }

      if (this.itemId === 'new') {
        this.item.type = this.itemType;
      }

      if (this.item.type === 'aws_sm' && this.item.params.use_iam_role === undefined) {
        // Storages created before the IAM role option always had an access key.
        const useIamRole = this.itemId !== 'new' && !this.item.params.access_key_id;
        this.$set(this.item.params, 'use_iam_role', useIamRole);
      }

      this.initialUseIamRole = !!this.item.params.use_iam_role;

      if (this.item.type === 'vault' || this.item.type === 'openbao') {
        this.$set(this.item.params, 'mount', this.item.params.mount || 'secret');
        this.$set(this.item.params, 'auth_method', this.item.params.auth_method || 'token');
        this.$set(this.item.params, 'timeout', this.item.params.timeout || '5s');
        this.$set(this.item, 'sync_direction', this.item.sync_direction || 'read_only');
        this.item.readonly = this.item.sync_direction !== 'outbound';
        if (this.item.sync_direction !== 'outbound') {
          this.item.sync_enabled = false;
          this.item.sync_interval = 0;
        }
        this.localKeys = await this.loadProjectResources('keys');
      }

      this.secretStorageReady = false;
      this.secretStorage = this.item.source_storage_type || 'database';
      this.$nextTick(() => {
        this.secretStorageReady = true;
      });
    },

    beforeSave() {
      if (this.useIamRole) {
        // Credentials are taken from the environment, don't send the disabled
        // secret fields to the server.
        this.item.secret = '';
        this.item.source_storage_type = undefined;
      }
    },

    getItemsUrl() {
      return `/api/project/${this.projectId}/secret_storages`;
    },

    getSingleItemUrl() {
      return `/api/project/${this.projectId}/secret_storages/${this.itemId}`;
    },

  },

  watch: {
    ...enhancedWatch,
    secretStorage(value, oldValue) {
      this.item.source_storage_type = value === 'database' ? undefined : value;

      if (!this.secretStorageReady || value === oldValue) {
        return;
      }

      this.item.secret = '';
    },
  },
};
</script>
<style lang="scss" scoped>
.aws-credentials--inactive {
  position: relative;
  &::after {
    content: "";
    position: absolute;
    background: white;
    left: 0;
    right: 0;
    top: 0;
    bottom: 0;
    opacity: 0.65;
  }
}

.theme--dark {
  .aws-credentials--inactive {
    position: relative;
    &::after {
      background: #1E1E1E;
    }
  }
}

</style>
