<template>
  <v-form
    ref="form"
    v-model="formValid"
    v-if="item != null && secretStorages != null"
    class="KeyForm"
    data-testid="key-form"
    :data-state="formSaving ? 'loading' : (formError ? 'error' : 'ready')"
  >
    <v-alert
      :model-value="!!formError"
      color="error"
      class="mb-6"
      data-testid="key-error"
    >{{ formError }}</v-alert>

    <v-text-field
      v-model="item.name"
      :label="$t('keyName')"
      :rules="[(v) => !!v || $t('name_required')]"
      required
      :disabled="formSaving"
      variant="outlined"
      density="compact"
      data-testid="key-name"
    />

    <v-card
      class="mb-6"
      :color="$vuetify.theme.current.dark ? '#212121' : 'white'"
      data-testid="key-source"
    >
      <v-tabs
        fixed-tabs
        v-model="sourceStorageTypeIndex"
        class="KeyForm__sourceTabs"
        data-testid="key-sourceTabs"
      >
        <v-tab
          :disabled="formSaving || !canEditSecrets || isSynced"
          style="padding: 0"
          data-testid="key-tab-local"
          >Local</v-tab
        >
        <v-tab
          :disabled="formSaving || !canEditSecrets || isSynced || !runtimeCanWrite"
          style="padding: 0"
          data-testid="key-tab-storage"
          >
            Storage
          </v-tab
        >
        <v-tab
          :disabled="formSaving || !canEditSecrets || isSynced"
          style="padding: 0"
          data-testid="key-tab-env"
        >Env</v-tab>
        <v-tab
          :disabled="formSaving || !canEditSecrets || isSynced"
          style="padding: 0"
          data-testid="key-tab-file"
        >File</v-tab>
      </v-tabs>

      <div
        :class="!supportStorages && sourceStorageType === 'vault' ? '' : 'ml-4 mr-4 mt-6'"
        v-if="sourceStorageType"
        data-testid="key-sourceBody"
      >
        <v-alert
          variant="tonal"
          color="hsl(348deg, 86%, 61%)"
          class="PageAlert PageAlert--flat-top mb-0"
          v-if="!supportStorages && sourceStorageType === 'vault'"
          data-testid="key-storageUpgradeAlert"
        >
          Storage-backed keys are not enabled.
        </v-alert>

        <v-autocomplete
          v-if="supportStorages && sourceStorageType === 'vault'"
          v-model="item.source_storage_id"
          :label="$t('Storage')"
          :items="runtimeSecretStorages"
          item-value="id"
          item-title="name"
          :disabled="formSaving || !canEditSecrets || isSynced || !runtimeCanWrite"
          data-testid="key-runtimeStorage"
          variant="outlined"
          density="compact"
          clearable
          :menu-props="storageMenu"
        />

        <RuntimeKeyReferenceFields
          :active="supportStorages && sourceStorageType === 'vault'
              && item.source_storage_id != null"
          :disabled="runtimeReferenceDisabled"
          v-model:mount="item.source_storage_mount"
          v-model:version="item.source_storage_version"
          v-model:field="item.source_storage_field"
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
            variant="outlined"
            density="compact"
          />
        </RuntimeKeyReferenceFields>

        <v-alert
          v-if="sourceStorageType === 'vault' && runtimeDecision
            && runtimeDecision.state !== 'active'"
          density="compact"
          variant="tonal"
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
          variant="outlined"
          density="compact"
          :data-testid="`key-sourceStorageKey-${sourceStorageType}`"
        />
      </div>
    </v-card>

    <v-select
      v-model="item.type"
      :label="$t('type')"
      :rules="[(v) => !!v || !canEditSecrets || $t('type_required')]"
      :items="inventoryTypes"
      item-value="id"
      item-title="name"
      :required="canEditSecrets"
      :disabled="formSaving || !canEditSecrets || runtimeTypeDisabled"
      variant="outlined"
      density="compact"
      data-testid="key-type"
    />

    <v-alert
      v-if="isReadOnly"
      type="info"
      variant="tonal"
      data-testid="key-readOnlyAlert"
    >Read-only secret storage chosen.</v-alert>
    <v-alert v-if="isNew && item.type === 'ssh' && sourceStorageType" type="info" variant="tonal">
      Server generation is available only for Local storage.
    </v-alert>

    <v-text-field
      v-model="item.login_password.login"
      :label="$t('usernameOptional')"
      v-if="!isReadOnly && item.type === 'login_password'"
      :disabled="formSaving || !canEditSecrets"
      variant="outlined"
      density="compact"
      data-testid="key-loginPasswordLogin"
    />

    <v-text-field
      v-model="item.login_password.password"
      :append-inner-icon="showLoginPassword ? 'mdi-eye' : 'mdi-eye-off'"
      :label="$t('password')"
      :rules="[(v) => !!v || !canEditSecrets || $t('password_required')]"
      :class="{ 'masked-secret-input': !showLoginPassword }"
      v-if="!isReadOnly && item.type === 'login_password'"
      :required="canEditSecrets"
      :disabled="formSaving || !canEditSecrets"
      autocomplete="new-password"
      @click:append-inner="showLoginPassword = !showLoginPassword"
      variant="outlined"
      density="compact"
      data-testid="key-loginPasswordPassword"
    />

    <v-text-field
      v-model="item.string"
      :append-inner-icon="showString ? 'mdi-eye' : 'mdi-eye-off'"
      :label="$t('keyFormStringValue')"
      :rules="[(v) => !!v || !canEditSecrets || $t('value_required')]"
      :class="{ 'masked-secret-input': !showString }"
      v-if="!isReadOnly && item.type === 'string'"
      :required="canEditSecrets"
      :disabled="formSaving || !canEditSecrets"
      autocomplete="new-password"
      @click:append-inner="showString = !showString"
      variant="outlined"
      density="compact"
      data-testid="key-stringValue"
    />

    <v-text-field
      v-model="item.ssh.login"
      :label="$t('usernameOptional')"
      v-if="!isReadOnly && item.type === 'ssh'"
      :disabled="formSaving || !canEditSecrets"
      variant="outlined"
      density="compact"
      data-testid="key-sshLogin"
    />

    <GeneratedSSHKeyControls
      :can-generate="canGenerateSSHKey"
      v-model:generate="generateSSHKey"
      v-model:algorithm="generatedSSHKeyAlgorithm"
      :algorithms="generatedSSHKeyAlgorithms"
      :metadata="generatedSSHKeyMetadata"
      :disabled="formSaving || !canEditSecrets"
    />

    <v-text-field
      v-model="item.ssh.passphrase"
      :append-inner-icon="showSSHPassphrase ? 'mdi-eye' : 'mdi-eye-off'"
      label="Passphrase (Optional)"
      :class="{ 'masked-secret-input': !showSSHPassphrase }"
      v-if="!isReadOnly && item.type === 'ssh' && !generateSSHKey"
      :disabled="formSaving || !canEditSecrets"
      @click:append-inner="showSSHPassphrase = !showSSHPassphrase"
      variant="outlined"
      density="compact"
      data-testid="key-sshPassphrase"
    />

    <v-textarea
      variant="outlined"
      v-model="item.ssh.private_key"
      :label="$t('privateKey')"
      :disabled="formSaving || !canEditSecrets"
      :rules="[(v) => !canEditSecrets || !!v || $t('private_key_required')]"
      v-if="!isReadOnly && item.type === 'ssh' && !generateSSHKey"
      class="KeyForm__privateKey"
      data-testid="key-sshPrivateKey"
    />

    <v-checkbox
      v-model="item.override_secret"
      :label="$t('override')"
      v-if="!isNew && !generatedSSHKeyMetadata"
        data-testid="key-overrideSecret"
    />

    <v-alert
      density="compact"
      variant="tonal"
      type="info"
      v-if="item.type === 'none'"
      data-testid="key-noneInfo"
    >
      {{ $t('useThisTypeOfKeyForHttpsRepositoriesAndForPlaybook') }}
    </v-alert>

    <v-alert
      density="compact"
      variant="tonal"
      type="info"
      v-if="item.type === 'string'"
      data-testid="key-stringInfo"
    >
      {{ $t('keyFormStringHint') }}
    </v-alert>
  </v-form>
</template>
<style lang="scss">
// The source card is white (#212121 in the dark theme) as in Vuetify 2, where the colour class
// of `color` hid the inline grey background the card had (removed: in Vuetify 4 it won).

// The source tabs share the card width equally, as `fixed-tabs` of Vuetify 2 did
// (`.v-tabs--fixed-tabs .v-tab { flex: 1 1 auto; width: 100% }`); Vuetify 4 sizes them by text.
.KeyForm__sourceTabs .v-tab {
  flex: 1 1 auto;
  width: 100%;
}

// The outlined textarea of Vuetify 2 (`.v-textarea textarea`): lines of 28 px, 10 px above the
// text, none below — 150 px for 5 rows (Vuetify 4: 24 px lines between 16 px paddings, 152 px).
.KeyForm__privateKey textarea.v-field__input {
  padding-top: 10px;
  padding-bottom: 0;
  line-height: 28px;
}

// The message of a disabled field (the stale "Private key is required" under Generate SSH Key)
// in the disabled text colour, not dimmed again (Vuetify 2: no error state while disabled; the
// grey outline — App.vue).
.KeyForm .v-input--disabled > .v-input__details {
  opacity: 1;

  .v-messages {
    color: rgba(var(--v-theme-on-surface), var(--v-disabled-opacity));
  }
}

// A disabled checkbox (Generate SSH Key, Override) in the disabled text colour
// (`.v-input--is-disabled` of Vuetify 2), not the medium emphasis dimmed to .38 again.
.KeyForm .v-selection-control--disabled {
  opacity: 1;

  .v-label,
  .v-selection-control__input > .v-icon {
    color: rgba(var(--v-theme-on-surface), var(--v-disabled-opacity));
    opacity: 1;
  }
}
</style>
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
      showString: false,
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
          id: 'string',
          name: `${this.$t('keyFormString')}`,
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
      // The list of the storage autocomplete 1 px under the field and at most 304 px high, as the
      // `offset-y` menu of Vuetify 2 (`menuProps`; Vuetify 4: flush with the field, 310 px).
      storageMenu: { maxHeight: 304, offset: 1 },
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
        this.item.source_storage_mount = 'secret';
      }
    },

    getNewItem() {
      return {
        ssh: {},
        login_password: {},
        source_storage_version: 0,
        string: '',
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
