<template>
  <div>
    <v-alert
      variant="tonal"
      class="SyncPathsEmpty"
      v-if="!paths || paths.length === 0"
      data-testid="secretStorageSync-empty"
    >
      {{ $t('No sync paths are defined.') }}
    </v-alert>
    <div
      v-for="(p, i) in paths"
      :key="i"
      class="d-flex align-start mb-4"
      data-testid="secretStorageSync-row"
    >
      <div style="flex: 1; min-width: 0" class="mb-2">
        <template v-if="managed"
          >
          <v-autocomplete
            v-model="p.access_key_id"
          :items="keys"
            item-value="id"
            item-title="name"
            label="Semaphore key"
            :rules="[(value) => !!value || 'Select a key']"
          variant="outlined"
          density="compact"
          @update:model-value="emitPaths"

          data-testid="secretStorageSync-path"
        />

          <div class="managed-secret-target">
            <v-text-field
              v-model="p.mount"
              label="KV v2 mount"
              :rules="[(value) => !!value || 'Mount is required']"
              variant="outlined"
              density="compact"
              @update:model-value="emitPaths"
            />
            <v-text-field
              v-model="p.path"
              label="Remote path"
              :rules="[(value) => !!value || 'Remote path is required']"
              :append-inner-icon="loader ? 'mdi-folder-search-outline' : undefined"
              variant="outlined"
              density="compact"
              data-testid="secretStorageSync-remotePath"
              @update:model-value="emitPaths"
              @click:append-inner="openBrowse(i)"
            />
            <v-text-field
              v-model="p.field"
              label="Remote field"
              :rules="[(value) => !!value || 'Remote field is required']"
              variant="outlined"
              density="compact"
              @update:model-value="emitPaths"
            />
          </div>
          <div v-if="p.remote_version" class="text-body-small mb-2">
            Last synchronized remote version: {{ p.remote_version }}
          </div>
        </template>

        <template v-else>
          <v-text-field
            class="mb-2"
            v-model="p.path"
            :label="$t('path')"
            variant="outlined"
            density="compact"
            @update:model-value="emitPaths"
            hide-details
            data-testid="secretStorageSync-separator"
          />
          <div class="d-flex" style="gap: 8px">
            <v-text-field
              v-model="p.separator"
              :label="$t('separator')"
              variant="outlined"
              density="compact"
              style="flex: 1"
              @update:model-value="emitPaths"
              hide-details
            />
            <v-text-field
            v-model="p.prefix"
            :label="$t('prefix')"
            variant="outlined"
            density="compact"
            style="flex: 1"
            @update:model-value="emitPaths"
            hide-details
            data-testid="secretStorageSync-prefix"
          />
        </div>
        </template>
      </div>

      <v-btn
        icon
        variant="text"
        class="ml-1 mt-1" aria-label="Remove sync mapping"
        @click="removePath(i)"
        data-testid="secretStorageSync-removePath"
      >
        <v-icon>mdi-delete</v-icon>
      </v-btn>
    </div>

    <v-btn
      variant="text"
      color="primary"
      @click="addPath"
      style="position: absolute; left: 12px; bottom: 12px"
      data-testid="secretStorageSync-addPath"
    >
      <v-icon start>mdi-plus</v-icon>
      Add path
    </v-btn>
    <v-dialog
      :model-value="browseIndex != null"
      max-width="680"
      @update:model-value="browseIndex = null"
    >
      <v-card v-if="browseIndex != null" data-testid="secretStorageSync-browse">
        <v-card-title>{{ $t('syncBrowseTitle') }}</v-card-title>
        <v-card-text>
          <SecretLocationPicker
            v-model="browseValue"
            store-type="vault"
            target="folder"
            :load="loader"
          />
          <div class="text-body-medium text-medium-emphasis">{{ $t('syncBrowseHint') }}</div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="browseIndex = null">{{ $t('cancel') }}</v-btn>
          <v-btn
            color="primary"
            variant="flat"
            :disabled="!browseValue.mount"
            data-testid="secretStorageSync-useFolder"
            @click="useBrowsedFolder"
          >
            {{ $t('syncBrowseUse') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script>
import SecretLocationPicker from '@/components/enhanced/SecretLocationPicker.vue';

export default {
  props: {
    // Lists the storage's mounts and folders; set for saved storages only.
    loader: {
      type: Function,
      default: null,
    },
    modelValue: {
      type: Array,
      default: () => [],
    },
    managed: {
      type: Boolean,
      default: false,
    },
    keys: {
      type: Array,
      default: () => [],
    },
    defaultMount: {
      type: String,
      default: 'secret',
    },
  },

  emits: ['update:modelValue'],

  components: { SecretLocationPicker },

  data() {
    return {
      paths: [],
      browseIndex: null,
      browseValue: {},
    };
  },

  created() {
    this.paths = this.modelValue && this.modelValue.length
      ? this.modelValue.map((p) => ({ ...p }))
      : [];
  },

  watch: {
    modelValue(val) {
      if (JSON.stringify(val) !== JSON.stringify(this.paths)) {
        this.paths = (val || []).map((p) => ({ ...p }));
      }
    },
  },

  methods: {
    openBrowse(index) {
      const current = this.paths[index];
      const folder = (current.path || '').includes('/')
        ? current.path.slice(0, current.path.lastIndexOf('/'))
        : '';
      this.browseValue = { mount: current.mount || this.defaultMount, path: folder };
      this.browseIndex = index;
    },
    // useBrowsedFolder places the mapping in the picked folder and keeps the
    // secret's own name, which may not exist yet: synchronization creates it.
    useBrowsedFolder() {
      const current = this.paths[this.browseIndex];
      const name = (current.path || '').split('/').pop();
      current.mount = this.browseValue.mount;
      current.path = [this.browseValue.path, name].filter((part) => part).join('/');
      this.browseIndex = null;
      this.emitPaths();
    },
    emitPaths() {
      this.$emit(
        'update:modelValue',
        this.paths.map((p) => ({ ...p })),
      );
    },

    addPath() {
      this.paths.push(
        this.managed
          ? {
            access_key_id: null, mount: this.defaultMount, path: '', field: '',
          }
          : { path: '', separator: '', prefix: '' },
      );
      this.emitPaths();
    },

    removePath(index) {
      this.paths.splice(index, 1);
      this.emitPaths();
    },
  },
};
</script>

<style scoped>
.managed-secret-target {
  display: grid;
  gap: 8px;
  grid-template-columns: minmax(90px, 0.7fr) minmax(150px, 1.5fr) minmax(100px, 1fr);
}

@media (max-width: 600px) {
  .managed-secret-target {
    display: block;
  }
}
</style>
<style lang="scss" scoped>
// The `v-alert text` of Vuetify 2 without a colour is a sheet of the theme: its text and its tint
// in the primary text colour (`.theme--light.v-sheet`: .87), not in the secondary text colour of
// the card around it, which a tonal alert of Vuetify 4 inherits.
.SyncPathsEmpty.v-theme--light {
  color: rgba(0, 0, 0, .87);
}

.SyncPathsEmpty.v-theme--dark {
  color: #fff;
}
</style>
