<template>
  <div class="SecretLocationPicker" data-testid="secret-picker">
    <v-row density="compact">
      <template v-if="isInfisical">
        <v-col cols="12" sm="6">
          <v-autocomplete
            :model-value="modelValue.project || null"
            :items="projects"
            item-title="name"
            item-value="value"
            :label="$t('secretPickerProject')"
            :loading="loading.root"
            :disabled="disabled"
            :no-data-text="$t('secretPickerNoProject')"
            data-testid="secret-picker-project"
            @update:model-value="selectProject"
          />
        </v-col>
        <v-col cols="12" sm="6">
          <v-autocomplete
            :model-value="modelValue.environment || null"
            :items="environments"
            item-title="name"
            item-value="value"
            :label="$t('secretPickerEnvironment')"
            :loading="loading.environments"
            :disabled="disabled || !modelValue.project"
            :no-data-text="$t('secretPickerNoEnvironment')"
            data-testid="secret-picker-environment"
            @update:model-value="selectEnvironment"
          />
        </v-col>
      </template>
      <v-col v-else cols="12">
        <v-autocomplete
          :model-value="modelValue.mount || null"
          :items="mounts"
          item-title="name"
          item-value="value"
          :label="$t('secretPickerMount')"
          :loading="loading.root"
          :disabled="disabled"
          :no-data-text="$t('secretPickerNoMount')"
          data-testid="secret-picker-mount"
          @update:model-value="selectMount"
        />
      </v-col>
    </v-row>

    <v-card
      v-if="containerReady"
      variant="outlined"
      class="mb-4"
      data-testid="secret-picker-browser"
    >
      <div class="d-flex align-center flex-wrap px-2 pt-1">
        <v-btn
          v-for="(crumb, index) in crumbs"
          :key="crumb.path"
          variant="text"
          size="small"
          class="text-none px-2"
          :prepend-icon="index === 0 ? 'mdi-folder-home-outline' : undefined"
          :disabled="disabled || index === crumbs.length - 1"
          :data-testid="`secret-picker-crumb-${index}`"
          @click="openFolder(crumb.path)"
        >
          {{ crumb.name }}
          <v-icon v-if="index < crumbs.length - 1" end size="small">mdi-chevron-right</v-icon>
        </v-btn>
      </div>
      <v-text-field
        v-model="filter"
        prepend-inner-icon="mdi-magnify"
        :placeholder="searchPlaceholder"
        density="compact"
        variant="outlined"
        hide-details
        clearable
        class="mx-3 mb-2"
        :disabled="disabled"
        data-testid="secret-picker-search"
      />
      <v-progress-linear v-if="loading.entries" indeterminate height="2" />
      <v-list
        density="compact"
        role="listbox"
        :aria-label="crumbs.map((crumb) => crumb.name).join(' / ')"
        :aria-busy="loading.entries ? 'true' : 'false'"
        class="SecretLocationPicker__entries py-0"
        data-testid="secret-picker-entries"
      >
        <v-list-item
          v-for="entry in visibleEntries"
          :key="`${entry.kind}:${entry.value}`"
          role="option"
          :aria-selected="isChosen(entry) ? 'true' : 'false'"
          :prepend-icon="entry.kind === 'folder' ? 'mdi-folder-outline' : 'mdi-key-outline'"
          :title="entry.name"
          :active="isChosen(entry)"
          :disabled="disabled || loading.entries"
          color="primary"
          :data-testid="`secret-picker-entry-${entry.kind}`"
          @click="choose(entry)"
        >
          <template v-slot:append>
            <v-icon v-if="entry.kind === 'folder'" size="small">mdi-chevron-right</v-icon>
            <v-icon v-else-if="isChosen(entry)" size="small" color="primary">mdi-check</v-icon>
          </template>
        </v-list-item>
        <v-list-item
          v-if="!loading.entries && visibleEntries.length === 0"
          :title="filter ? $t('secretPickerNoMatch', { filter }) : emptyText"
          class="text-medium-emphasis"
          data-testid="secret-picker-empty"
        />
      </v-list>
    </v-card>

    <v-autocomplete
      v-if="needsField && modelValue.secret"
      :model-value="modelValue.field || null"
      :items="fields"
      item-title="name"
      item-value="value"
      :label="$t('secretPickerField')"
      :loading="loading.fields"
      :disabled="disabled"
      :no-data-text="$t('secretPickerNoField')"
      data-testid="secret-picker-field"
      @update:model-value="selectField"
    />

    <v-alert
      v-if="error"
      type="warning"
      variant="tonal"
      density="compact"
      class="mb-4"
      data-testid="secret-picker-error"
    >
      {{ error.message }}
      <template v-slot:append>
        <v-btn
          variant="text"
          size="small"
          data-testid="secret-picker-manual"
          @click="$emit('manual')"
        >
          {{ $t('secretPickerManual') }}
        </v-btn>
      </template>
    </v-alert>

    <div
      role="status"
      aria-live="polite"
      :class="summary ? 'text-body-medium mb-4' : ''"
      data-testid="secret-picker-summary"
    >
      <template v-if="summary">
        <v-icon size="small" class="mr-1">{{ summaryIcon }}</v-icon>
        {{ summary }}
      </template>
    </div>
  </div>
</template>

<script>
import { browseError, locationLabel } from '@/lib/enhanced/secret-browse';

// SecretLocationPicker lets people pick a project, environment, folder and
// secret (Infisical) or a mount, folder, secret and field (Vault/OpenBao) by
// name instead of typing identifiers. It lists one level at a time through
// `load`, a function from a location to value-free entries.
export default {
  emits: ['update:modelValue', 'manual', 'summary'],
  props: {
    // { project, environment, mount, path, secret, field, version }
    modelValue: { type: Object, required: true },
    storeType: { type: String, required: true },
    load: { type: Function, required: true },
    // 'secret' picks one secret; 'folder' picks a folder, e.g. a scope.
    target: { type: String, default: 'secret' },
    disabled: { type: Boolean, default: false },
  },
  data() {
    return {
      projects: [],
      environments: [],
      mounts: [],
      entries: [],
      fields: [],
      folder: '',
      filter: '',
      error: null,
      loading: {
        root: false, environments: false, entries: false, fields: false,
      },
      request: 0,
    };
  },
  computed: {
    isInfisical() {
      return this.storeType === 'infisical';
    },
    needsField() {
      return !this.isInfisical && this.target === 'secret';
    },
    containerReady() {
      return this.isInfisical
        ? !!(this.modelValue.project && this.modelValue.environment)
        : !!this.modelValue.mount;
    },
    containerName() {
      if (this.isInfisical) {
        const environment = this.environments.find((e) => e.value === this.modelValue.environment);
        return environment ? environment.name : this.modelValue.environment;
      }
      return this.modelValue.mount;
    },
    crumbs() {
      const crumbs = [{ name: this.containerName || this.$t('secretPickerRoot'), path: '' }];
      let path = '';
      (this.folder ? this.folder.split('/') : []).forEach((segment) => {
        path = path ? `${path}/${segment}` : segment;
        crumbs.push({ name: segment, path });
      });
      return crumbs;
    },
    visibleEntries() {
      const needle = (this.filter || '').toLowerCase();
      return this.entries
        .filter((entry) => this.target === 'secret' || entry.kind === 'folder')
        .filter((entry) => !needle || entry.name.toLowerCase().includes(needle));
    },
    searchPlaceholder() {
      return this.target === 'folder'
        ? this.$t('secretPickerSearchFolders')
        : this.$t('secretPickerSearchAll');
    },
    summaryIcon() {
      return this.target === 'folder' ? 'mdi-folder-check-outline' : 'mdi-check-circle-outline';
    },
    emptyText() {
      return this.target === 'folder' ? this.$t('secretPickerEmptyFolders') : this.$t('secretPickerEmpty');
    },
    // location names the picked location for people; summary shows it.
    location() {
      if (!this.containerReady) {
        return '';
      }
      const project = this.projects.find((p) => p.value === this.modelValue.project);
      const head = this.isInfisical
        ? [project ? project.name : this.modelValue.project, this.containerName]
        : [this.modelValue.mount];
      if (this.target === 'folder') {
        return locationLabel([...head, this.modelValue.path || this.$t('secretPickerAllFolders')]);
      }
      if (!this.modelValue.secret) {
        return '';
      }
      const secret = this.isInfisical
        ? locationLabel([this.modelValue.path, this.modelValue.secret])
        : this.modelValue.secret;
      return locationLabel([...head, secret, this.needsField ? this.modelValue.field : '']);
    },
    summary() {
      if (!this.location) {
        return '';
      }
      return this.target === 'folder' ? this.$t('secretPickerScope', { location: this.location }) : this.location;
    },
  },
  watch: {
    location: {
      immediate: true,
      handler(value) {
        this.$emit('summary', value);
      },
    },
    load() {
      this.reset();
    },
    storeType() {
      this.reset();
    },
  },
  mounted() {
    this.reset();
  },
  methods: {
    emit(changes) {
      this.$emit('update:modelValue', { ...this.modelValue, ...changes });
    },
    async fetch(kind, location) {
      this.request += 1;
      const request = this.request;
      this.loading[kind] = true;
      this.error = null;
      try {
        const nodes = await this.load(location);
        return request === this.request ? (nodes || []) : null;
      } catch (error) {
        if (request === this.request) {
          this.error = browseError(error);
        }
        return null;
      } finally {
        this.loading[kind] = false;
      }
    },
    async reset() {
      this.projects = [];
      this.environments = [];
      this.mounts = [];
      this.entries = [];
      this.fields = [];
      const root = await this.fetch('root', {});
      if (root) {
        if (this.isInfisical) {
          this.projects = root;
        } else {
          this.mounts = root;
        }
      }
      if (this.isInfisical && this.modelValue.project) {
        await this.loadEnvironments();
      }
      if (this.containerReady) {
        await this.openFolder(this.initialFolder());
        if (this.needsField && this.modelValue.secret) {
          await this.loadFields();
        }
      }
    },
    initialFolder() {
      if (this.target === 'folder' || this.isInfisical) {
        return this.modelValue.path || '';
      }
      const secret = this.modelValue.secret || '';
      return secret.includes('/') ? secret.slice(0, secret.lastIndexOf('/')) : '';
    },
    container() {
      return this.isInfisical
        ? { project: this.modelValue.project, environment: this.modelValue.environment }
        : { mount: this.modelValue.mount };
    },
    async loadEnvironments() {
      const nodes = await this.fetch('environments', { project: this.modelValue.project });
      if (nodes) {
        this.environments = nodes;
      }
    },
    async openFolder(path) {
      this.folder = path;
      this.filter = '';
      // Entries of the previous folder must not be chosen for the new one.
      this.entries = [];
      if (this.target === 'folder') {
        this.emit({ path });
      }
      const nodes = await this.fetch('entries', { ...this.container(), path });
      if (nodes) {
        this.entries = nodes;
      }
    },
    async loadFields() {
      const nodes = await this.fetch('fields', { mount: this.modelValue.mount, secret: this.modelValue.secret });
      if (nodes) {
        this.fields = nodes;
        if (nodes.length === 1 && !this.modelValue.field) {
          this.selectField(nodes[0].value);
        }
      }
    },
    async selectProject(project) {
      this.environments = [];
      this.entries = [];
      this.emit({
        project: project || '', environment: '', path: '', secret: '', version: 0,
      });
      if (project) {
        await this.$nextTick();
        await this.loadEnvironments();
      }
    },
    async selectEnvironment(environment) {
      this.emit({ environment: environment || '', path: '', secret: '' });
      await this.$nextTick();
      if (environment) {
        await this.openFolder('');
      }
    },
    async selectMount(mount) {
      this.entries = [];
      this.fields = [];
      this.emit({
        mount: mount || '', path: '', secret: '', field: '', version: 0,
      });
      await this.$nextTick();
      if (mount) {
        await this.openFolder('');
      }
    },
    isChosen(entry) {
      if (entry.kind !== 'secret') {
        return false;
      }
      return this.isInfisical
        ? entry.value === this.modelValue.secret && this.folder === (this.modelValue.path || '')
        : entry.value === this.modelValue.secret;
    },
    async choose(entry) {
      if (entry.kind === 'folder') {
        await this.openFolder(entry.value);
        return;
      }
      if (this.isInfisical) {
        this.emit({ path: this.folder, secret: entry.value, version: 0 });
        return;
      }
      this.fields = [];
      this.emit({ secret: entry.value, field: '', version: entry.version || 0 });
      await this.$nextTick();
      await this.loadFields();
    },
    selectField(field) {
      const node = this.fields.find((f) => f.value === field);
      const changes = { field: field || '' };
      if (node && node.version) {
        changes.version = node.version;
      }
      this.emit(changes);
    },
  },
};
</script>

<style lang="scss">
.SecretLocationPicker__entries {
  max-height: 280px;
  overflow-y: auto;
}
</style>
