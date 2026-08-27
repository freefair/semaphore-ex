<template>
  <v-form
    ref="form"
    lazy-validation
    v-model="formValid"
    v-if="item != null && secretStorages != null"
  >
    <v-alert :value="formError" color="error" class="mb-6">{{ formError }}</v-alert>

    <v-text-field
      v-model="item.name"
      :label="$t('keyName')"
      :rules="[(v) => !!v || $t('name_required')]"
      required
      :disabled="formSaving"
      outlined
      dense
    />

    <v-card
      class="mb-6"
      :color="$vuetify.theme.dark ? '#212121' : 'white'"
      style="background: #8585850f"
    >
      <v-tabs fixed-tabs v-model="sourceStorageTypeIndex">
        <v-tab :disabled="formSaving || !canEditSecrets || isSynced" style="padding: 0"
          >Local</v-tab
        >
        <v-tab
          v-if="isPro"
          :disabled="formSaving || !canEditSecrets || isSynced || !runtimeCanWrite"
          style="padding: 0"
          >
            Storage
          </v-tab
        >
        <v-tab :disabled="formSaving || !canEditSecrets || isSynced" style="padding: 0">Env</v-tab>
        <v-tab :disabled="formSaving || !canEditSecrets || isSynced" style="padding: 0">File</v-tab>
      </v-tabs>

      <div
        :class="!supportStorages && sourceStorageType === 'vault' ? '' : 'ml-4 mr-4 mt-6'"
        v-if="sourceStorageType"
      >
        <v-alert
          text
          color="hsl(348deg, 86%, 61%)"
          class="PageAlert PageAlert--flat-top"
          v-if="!supportStorages && sourceStorageType === 'vault'"
        >
          <span v-html="$t('project_runners_only_pro')"></span>
          <v-btn dark class="ml-2" color="hsl(348deg, 86%, 61%)" @click="upgradeToPro()">
            {{ $t('upgrade_to_pro') }}
          </v-btn>
        </v-alert>

        <v-autocomplete
          v-if="supportStorages && sourceStorageType === 'vault'"
          v-model="item.source_storage_id"
          :label="$t('Storage')"
          :items="runtimeSecretStorages"
          item-value="id"
          item-text="name"
          :disabled="formSaving || !canEditSecrets || isSynced || !runtimeCanWrite"
          data-testid="key-runtimeStorage"
          outlined
          dense
          clearable
        />

        <v-text-field
          v-if="supportStorages && sourceStorageType === 'vault' && item.source_storage_id != null"
          v-model="item.source_storage_mount"
          label="KV v2 mount"
          :rules="[(v) => !!v || 'Mount is required']"
          :disabled="runtimeReferenceDisabled"
          data-testid="key-runtimeMount"
          outlined
          dense
        />

        <v-text-field
          v-if="supportStorages && sourceStorageType === 'vault' && item.source_storage_id != null"
          v-model="item.source_storage_key"
          label="Secret path"
          hint="Path within the mount; values are not browsed"
          :rules="[(v) => !!v || 'Secret path is required']"
          :disabled="runtimeReferenceDisabled"
          data-testid="key-runtimePath"
          outlined
          dense
        />

        <v-row
          v-if="supportStorages && sourceStorageType === 'vault' && item.source_storage_id != null"
        >
          <v-col cols="12" sm="5">
            <v-text-field
              v-model.number="item.source_storage_version"
              label="Version (optional)"
              type="number"
              min="0"
              :rules="[(v) => v == null || v >= 0 || 'Version must not be negative']"
              :disabled="runtimeReferenceDisabled"
              data-testid="key-runtimeVersion"
              outlined
              dense
            />
          </v-col>
          <v-col cols="12" sm="7">
            <v-text-field
              v-model="item.source_storage_field"
              label="Field"
              :rules="[(v) => !!v || 'Field is required']"
              :disabled="runtimeReferenceDisabled"
              data-testid="key-runtimeField"
              outlined
              dense
            />
          </v-col>
        </v-row>

        <v-alert
          v-if="sourceStorageType === 'vault' && runtimeDecision
            && runtimeDecision.state !== 'active'"
          dense
          text
          :type="runtimeDecision.state === 'read_only' ? 'info' : 'warning'"
          data-testid="key-runtimeCapability"
        >
          Runtime secrets are {{ runtimeDecision.state.replace('_', ' ') }}.
          <template v-if="runtimeDecision.state === 'read_only'">
            Existing references remain executable, but changes are blocked by the server.
          </template>
          <template v-else>
            Existing references remain visible, but resolution and changes are blocked by the
            server.
          </template>
        </v-alert>

        <v-text-field
          v-if="['env', 'file'].includes(sourceStorageType)"
          v-model="item.source_storage_key"
          :label="
            sourceStorageType === 'env' ? $t('Environment variable name') : $t('Path to the file')
          "
          :rules="[(v) => !!v || $t('type_required')]"
          :disabled="formSaving || !canEditSecrets"
          outlined
          dense
        />
      </div>
    </v-card>

    <v-select
      v-model="item.type"
      :label="$t('type')"
      :rules="[(v) => !!v || !canEditSecrets || $t('type_required')]"
      :items="inventoryTypes"
      item-value="id"
      item-text="name"
      :required="canEditSecrets"
      :disabled="formSaving || !canEditSecrets || runtimeTypeDisabled"
      outlined
      dense
    />

    <v-alert v-if="isReadOnly" type="info" text>Read-only secret storage chosen.</v-alert>

    <v-text-field
      v-model="item.login_password.login"
      :label="$t('usernameOptional')"
      v-if="!isReadOnly && item.type === 'login_password'"
      :disabled="formSaving || !canEditSecrets"
      outlined
      dense
    />

    <v-text-field
      v-model="item.login_password.password"
      :append-icon="showLoginPassword ? 'mdi-eye' : 'mdi-eye-off'"
      :label="$t('password')"
      :rules="[(v) => !!v || !canEditSecrets || $t('password_required')]"
      :class="{ 'masked-secret-input': !showLoginPassword }"
      v-if="!isReadOnly && item.type === 'login_password'"
      :required="canEditSecrets"
      :disabled="formSaving || !canEditSecrets"
      autocomplete="new-password"
      @click:append="showLoginPassword = !showLoginPassword"
      outlined
      dense
    />

    <v-text-field
      v-model="item.ssh.login"
      :label="$t('usernameOptional')"
      v-if="!isReadOnly && item.type === 'ssh'"
      :disabled="formSaving || !canEditSecrets"
      outlined
      dense
    />

    <v-text-field
      v-model="item.ssh.passphrase"
      :append-icon="showSSHPassphrase ? 'mdi-eye' : 'mdi-eye-off'"
      label="Passphrase (Optional)"
      :class="{ 'masked-secret-input': !showSSHPassphrase }"
      v-if="!isReadOnly && item.type === 'ssh'"
      :disabled="formSaving || !canEditSecrets"
      @click:append="showSSHPassphrase = !showSSHPassphrase"
      outlined
      dense
    />

    <v-textarea
      outlined
      v-model="item.ssh.private_key"
      :label="$t('privateKey')"
      :disabled="formSaving || !canEditSecrets"
      :rules="[(v) => !canEditSecrets || !!v || $t('private_key_required')]"
      v-if="!isReadOnly && item.type === 'ssh'"
    />

    <v-checkbox v-model="item.override_secret" :label="$t('override')" v-if="!isNew" />

    <v-alert dense text type="info" v-if="item.type === 'none'">
      {{ $t('useThisTypeOfKeyForHttpsRepositoriesAndForPlaybook') }}
    </v-alert>
  </v-form>
</template>
<script>
import ItemFormBase from '@/components/ItemFormBase';
import { findCapabilityDecision } from '@/lib/capabilities';

export default {
  mixins: [ItemFormBase],

  props: {
    supportStorages: Boolean,
    systemInfo: Object,
  },

  data() {
    return {
      showLoginPassword: false,
      showSSHPassphrase: false,
      inventoryTypes: [
        {
          id: 'ssh',
          name: `${this.$t('keyFormSshKey')}`,
        },
        {
          id: 'login_password',
          name: `${this.$t('keyFormLoginPassword')}`,
        },
        {
          id: 'none',
          name: `${this.$t('keyFormNone')}`,
        },
      ],
      secretStorages: null,
      isSynced: false,
    };
  },

  computed: {

    isPro() {
      return (process.env.VUE_APP_BUILD_TYPE || '').startsWith('pro_');
    },

    sourceStorageType() {
      return this.item?.source_storage_type;
    },

    runtimeDecision() {
      return findCapabilityDecision(this.systemInfo, 'runtime_secrets');
    },

    runtimeCanExecute() {
      return this.runtimeDecision?.access?.includes('execute') || false;
    },

    runtimeCanWrite() {
      return this.runtimeDecision?.access?.includes('write') || false;
    },

    runtimeReferenceDisabled() {
      return this.formSaving || !this.canEditSecrets || this.isSynced || !this.runtimeCanWrite;
    },

    runtimeTypeDisabled() {
      return this.sourceStorageType === 'vault' && !this.runtimeCanWrite;
    },

    runtimeSecretStorages() {
      return (this.secretStorages || []).filter((storage) => (
        storage.type === 'vault' || storage.type === 'openbao'
      ));
    },

    sourceStorageTypeIndex: {
      get() {
        return (
          {
            vault: 1,
            env: 2,
            file: 3,
          }[this.item.source_storage_type] || 0
        );
      },
      set(index) {
        this.item = {
          ...this.item,
          source_storage_type: [undefined, 'vault', 'env', 'file'][index],
        };
      },
    },

    canEditSecrets() {
      return this.isNew || this.item.override_secret;
    },

    isReadOnly() {
      if (!this.sourceStorageType) {
        return false;
      }

      if (['env', 'file'].includes(this.sourceStorageType)) {
        return true;
      }

      if (this.item.source_storage_id == null) {
        return false;
      }

      const storage = this.secretStorages.find((s) => s.id === this.item.source_storage_id);
      if (storage == null) {
        return false;
      }

      return storage.readonly;
    },
  },

  watch: {
    'item.source_storage_id': {
      handler(storageId) {
        if (this.item?.source_storage_type !== 'vault' || storageId == null) {
          return;
        }
        const storage = this.runtimeSecretStorages.find((candidate) => candidate.id === storageId);
        if (storage && !this.item.source_storage_mount) {
          this.$set(this.item, 'source_storage_mount', storage.params?.mount || 'secret');
        }
      },
    },
  },

  async created() {
    [this.secretStorages] = await Promise.all([this.loadProjectResources('secret_storages')]);
  },

  methods: {
    afterLoadData() {
      this.isSynced = JSON.parse(this.item.plain || '{}').dvls_id != null;
      if (this.item.source_storage_type === 'vault' && !this.item.source_storage_mount) {
        this.$set(this.item, 'source_storage_mount', 'secret');
      }
    },

    getNewItem() {
      return {
        ssh: {},
        login_password: {},
        source_storage_version: 0,
      };
    },

    getItemsUrl() {
      return `/api/project/${this.projectId}/keys`;
    },

    getSingleItemUrl() {
      return `/api/project/${this.projectId}/keys/${this.itemId}`;
    },
  },
};
</script>
