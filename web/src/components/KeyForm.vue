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
          Storage-backed keys are not enabled.
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

        <RuntimeKeyReferenceFields
          :active="supportStorages && sourceStorageType === 'vault'
              && item.source_storage_id != null"
          :disabled="runtimeReferenceDisabled"
          :mount.sync="item.source_storage_mount"
          :version.sync="item.source_storage_version"
          :field.sync="item.source_storage_field"
        >
          <v-text-field
            v-if="supportStorages && sourceStorageType === 'vault'
              && item.source_storage_id != null"
            v-model="item.source_storage_key"
            label="Secret path"
            hint="Path within the mount; values are not browsed"
            :rules="[(v) => !!v || 'Secret path is required']"
            :disabled="runtimeReferenceDisabled"
            data-testid="key-runtimePath"
            outlined
            dense
          />
        </RuntimeKeyReferenceFields>

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
    <v-alert v-if="isNew && item.type === 'ssh' && sourceStorageType" type="info" text>
      Server generation is available only for Local storage.
    </v-alert>

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

    <GeneratedSSHKeyControls
      :can-generate="canGenerateSSHKey"
      :generate.sync="generateSSHKey"
      :algorithm.sync="generatedSSHKeyAlgorithm"
      :algorithms="generatedSSHKeyAlgorithms"
      :metadata="generatedSSHKeyMetadata"
      :disabled="formSaving || !canEditSecrets"
    />

    <v-text-field
      v-model="item.ssh.passphrase"
      :append-icon="showSSHPassphrase ? 'mdi-eye' : 'mdi-eye-off'"
      label="Passphrase (Optional)"
      :class="{ 'masked-secret-input': !showSSHPassphrase }"
      v-if="!isReadOnly && item.type === 'ssh' && !generateSSHKey"
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
      v-if="!isReadOnly && item.type === 'ssh' && !generateSSHKey"
    />

    <v-checkbox
      v-model="item.override_secret"
      :label="$t('override')"
      v-if="!isNew && !generatedSSHKeyMetadata"
    />

    <v-alert dense text type="info" v-if="item.type === 'none'">
      {{ $t('useThisTypeOfKeyForHttpsRepositoriesAndForPlaybook') }}
    </v-alert>
  </v-form>
</template>
<script>
import enhancedWatch from '@/lib/enhanced/key-form-state';

import RuntimeKeyReferenceFields from '@/components/enhanced/RuntimeKeyReferenceFields.vue';
import GeneratedSSHKeyControls from '@/components/enhanced/GeneratedSSHKeyControls.vue';
import { enhancedComputed, enhancedMethods } from '@/lib/enhanced/key-form';

import ItemFormBase from '@/components/ItemFormBase';

export default {
  mixins: [ItemFormBase],

  components: {
    RuntimeKeyReferenceFields,
    GeneratedSSHKeyControls,
  },

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
      generateSSHKey: false,
      generatedSSHKeyAlgorithm: 'ed25519',
      generatedSSHKeyAlgorithms: [
        { id: 'ed25519', name: 'Ed25519 (recommended)' },
        { id: 'rsa-3072', name: 'RSA 3072 (compatibility)' },
      ],
    };
  },

  computed: {
    ...enhancedComputed,

    sourceStorageType() {
      return this.item?.source_storage_type;
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
        const sourceStorageType = [undefined, 'vault', 'env', 'file'][index];
        this.item = {
          ...this.item,
          source_storage_type: sourceStorageType,
        };
        if (sourceStorageType) {
          this.generateSSHKey = false;
        }
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
    ...enhancedWatch,
  },

  async created() {
    [this.secretStorages] = await Promise.all([this.loadProjectResources('secret_storages')]);
  },

  methods: {
    ...enhancedMethods,
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
