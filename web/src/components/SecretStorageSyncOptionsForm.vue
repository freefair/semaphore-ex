<template>
  <div>
    <v-alert text v-if="!paths || paths.length === 0">
      {{ $t('No sync paths are defined.') }}
    </v-alert>
    <div v-for="(p, i) in paths" :key="i" class="d-flex align-start mb-4">
      <div style="flex: 1; min-width: 0" class="mb-2">
        <template v-if="managed">
          <v-autocomplete
            v-model="p.access_key_id"
            :items="keys"
            item-value="id"
            item-text="name"
            label="Semaphore key"
            :rules="[(value) => !!value || 'Select a key']"
            outlined
            dense
            @change="emitPaths"
          />

          <div class="managed-secret-target">
            <v-text-field
              v-model="p.mount"
              label="KV v2 mount"
              :rules="[(value) => !!value || 'Mount is required']"
              outlined
              dense
              @input="emitPaths"
            />
            <v-text-field
              v-model="p.path"
              label="Remote path"
              :rules="[(value) => !!value || 'Remote path is required']"
              outlined
              dense
              @input="emitPaths"
            />
            <v-text-field
              v-model="p.field"
              label="Remote field"
              :rules="[(value) => !!value || 'Remote field is required']"
              outlined
              dense
              @input="emitPaths"
            />
          </div>
          <div v-if="p.remote_version" class="text-caption mb-2">
            Last synchronized remote version: {{ p.remote_version }}
          </div>
        </template>

        <template v-else>
          <v-text-field
            class="mb-2"
            v-model="p.path"
            :label="$t('path')"
            outlined
            dense
            @input="emitPaths"
            hide-details
          />

          <div class="d-flex" style="gap: 8px">
            <v-text-field
              v-model="p.separator"
              :label="$t('separator')"
              outlined
              dense
              style="flex: 1"
              @input="emitPaths"
              hide-details
            />
            <v-text-field
              v-model="p.prefix"
              :label="$t('prefix')"
              outlined
              dense
              style="flex: 1"
              @input="emitPaths"
              hide-details
            />
          </div>
        </template>
      </div>

      <v-btn icon class="ml-1 mt-1" aria-label="Remove sync mapping" @click="removePath(i)">
        <v-icon>mdi-delete</v-icon>
      </v-btn>
    </div>

    <v-btn
      text
      color="primary"
      @click="addPath"
      style="position: absolute; left: 12px; bottom: 12px"
    >
      <v-icon left>mdi-plus</v-icon>
      Add path
    </v-btn>
  </div>
</template>

<script>
export default {
  props: {
    value: {
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

  data() {
    return {
      paths: [],
    };
  },

  created() {
    this.paths = this.value && this.value.length ? this.value.map((p) => ({ ...p })) : [];
  },

  watch: {
    value(val) {
      if (JSON.stringify(val) !== JSON.stringify(this.paths)) {
        this.paths = (val || []).map((p) => ({ ...p }));
      }
    },
  },

  methods: {
    emitPaths() {
      this.$emit(
        'input',
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
