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
              variant="outlined"
              density="compact"
              @update:model-value="emitPaths"
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
  </div>
</template>

<script>
export default {
  props: {
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

  data() {
    return {
      paths: [],
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
