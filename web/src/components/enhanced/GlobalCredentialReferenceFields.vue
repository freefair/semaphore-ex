<template>
  <div data-testid="credential-reference-fields">
    <v-select
      :model-value="sourceKey"
      :items="sourceItems"
      item-title="title"
      item-value="value"
      label="Source"
      :no-data-text="'No provider is configured'"
      data-testid="credential-reference-source"
      @update:model-value="selectSource"
    />
    <v-alert
      v-if="selected && selected.source === 'database' && scopes.length === 0"
      type="info"
      variant="tonal"
      density="compact"
      class="mb-4"
      data-testid="credential-reference-no-scopes"
    >
      This connection may not read any secret yet. An administrator adds secret scopes
      under Administration > Global secret storages.
    </v-alert>
    <SecretLocationPicker
      v-else-if="browsable && !manual"
      :key="sourceKey"
      :model-value="pickerValue"
      :store-type="form.provider"
      :load="loader"
      data-testid="credential-reference-picker"
      @update:model-value="fromPicker"
      @manual="manual = true"
    />
    <template v-else-if="form.providerId">
      <v-alert
        v-if="browsable"
        type="info"
        variant="tonal"
        density="compact"
        class="mb-4"
      >
        Entering the location by hand.
        <template v-slot:append>
          <v-btn
            variant="text"
            size="small"
            data-testid="credential-reference-pick"
            @click="manual = false"
          >
            Pick from the store
          </v-btn>
        </template>
      </v-alert>
      <template v-if="isInfisical">
        <v-text-field
          :model-value="form.mount"
          label="Infisical project ID"
          data-testid="credential-reference-infisical-project"
          @update:model-value="update('mount', $event)"
        />
        <v-text-field
          :model-value="form.environment"
          label="Environment slug"
          placeholder="prod"
          data-testid="credential-reference-infisical-environment"
          @update:model-value="update('environment', $event)"
        />
        <v-text-field
          :model-value="form.path"
          label="Folder (optional)"
          placeholder="apps/web"
          :rules="[() => inScope || 'Outside the scopes of this connection']"
          data-testid="credential-reference-infisical-folder"
          @update:model-value="update('path', $event)"
        />
        <v-text-field
          :model-value="form.field"
          label="Secret name"
          placeholder="DB_PASSWORD"
          data-testid="credential-reference-infisical-name"
          @update:model-value="update('field', $event)"
        />
      </template>
      <template v-else>
        <v-text-field
          :model-value="form.mount"
          label="KV v2 mount"
          data-testid="credential-reference-mount"
          @update:model-value="update('mount', $event)"
        />
        <v-text-field
          :model-value="form.path"
          label="Secret path"
          data-testid="credential-reference-path"
          @update:model-value="update('path', $event)"
        />
        <v-text-field
          :model-value="form.field"
          label="Field"
          data-testid="credential-reference-field"
          @update:model-value="update('field', $event)"
        />
      </template>
    </template>
    <v-text-field
      v-if="form.providerId && !isInfisical"
      :model-value="form.version"
      label="Secret version"
      type="number"
      min="1"
      hint="Global credentials pin a version; picking a field fills in the current one."
      persistent-hint
      data-testid="credential-reference-version"
      @update:model-value="update('version', Number($event))"
    />
  </div>
</template>

<script>
import SecretLocationPicker from '@/components/enhanced/SecretLocationPicker.vue';
import { providerLoader } from '@/lib/enhanced/secret-browse';

const PROVIDER_LABELS = { vault: 'Vault', openbao: 'OpenBao', infisical: 'Infisical' };

// Mirrors the server's scope check so a reference outside every scope is
// caught in the form; the server remains authoritative.
export function referenceInScope(form, scopes) {
  return (scopes || []).some((scope) => scope.project_id === form.mount
    && scope.environment === form.environment
    && (!scope.path || form.path === scope.path || (form.path || '').startsWith(`${scope.path}/`)));
}

export function referenceValid(form, scopes) {
  if (form.provider === 'infisical') {
    return form.providerId !== '' && form.mount !== '' && form.environment !== ''
      && form.field !== '' && Number(form.version) >= 0 && referenceInScope(form, scopes);
  }
  return form.providerId !== '' && form.mount !== '' && form.path !== ''
    && Number(form.version) > 0 && form.field !== '';
}

export function referencePayload(form) {
  const reference = {
    provider: form.provider,
    provider_id: form.providerId,
    mount: form.mount,
    path: form.path,
    version: Number(form.version),
    field: form.field,
  };
  if (form.provider === 'infisical') {
    reference.environment = form.environment;
  }
  return reference;
}

export default {
  components: { SecretLocationPicker },
  emits: ['update:form'],
  props: {
    form: { type: Object, required: true },
    // Value-free provider identities from GET /api/global-credential-providers.
    providers: { type: Array, default: () => [] },
  },
  data() {
    return { manual: false };
  },
  computed: {
    selected() {
      return this.providers.find((p) => p.provider === this.form.provider
        && p.provider_id === this.form.providerId) || null;
    },
    // Connections managed under Administration can be browsed; providers
    // from the configuration file are entered by hand.
    browsable() {
      return !!this.selected && this.selected.source === 'database';
    },
    loader() {
      return providerLoader(this.form.providerId);
    },
    pickerValue() {
      if (this.isInfisical) {
        return {
          project: this.form.mount,
          environment: this.form.environment,
          path: this.form.path,
          secret: this.form.field,
        };
      }
      return {
        mount: this.form.mount,
        secret: this.form.path,
        field: this.form.field,
        version: this.form.version,
      };
    },
    isInfisical() {
      return this.form.provider === 'infisical';
    },
    scopes() {
      return (this.selected && this.selected.scopes) || [];
    },
    inScope() {
      return referenceInScope(this.form, this.scopes);
    },
    sourceKey() {
      return this.form.providerId ? `${this.form.provider}|${this.form.providerId}` : null;
    },
    sourceItems() {
      return this.providers.map((provider) => ({
        title: `${provider.name} (${PROVIDER_LABELS[provider.provider] || provider.provider}${provider.source === 'configuration' ? ', configuration file' : ''})`,
        value: `${provider.provider}|${provider.provider_id}`,
      }));
    },
  },
  methods: {
    fromPicker(value) {
      if (this.isInfisical) {
        this.$emit('update:form', {
          ...this.form,
          mount: value.project,
          environment: value.environment,
          path: value.path || '',
          field: value.secret || '',
          version: 0,
        });
        return;
      }
      this.$emit('update:form', {
        ...this.form,
        mount: value.mount,
        path: value.secret || '',
        field: value.field || '',
        version: Number(value.version) || this.form.version,
      });
    },
    update(key, value) {
      this.$emit('update:form', { ...this.form, [key]: value });
    },
    selectSource(value) {
      this.manual = false;
      const [provider, providerId] = (value || '|').split('|');
      const switching = provider !== this.form.provider;
      this.$emit('update:form', {
        ...this.form,
        provider,
        providerId,
        // A new source starts empty; Vault/OpenBao pin a version, Infisical
        // reads the current one.
        mount: switching || providerId !== this.form.providerId ? '' : this.form.mount,
        path: switching || providerId !== this.form.providerId ? '' : this.form.path,
        field: switching || providerId !== this.form.providerId ? '' : this.form.field,
        version: switching ? Number(provider !== 'infisical') : this.form.version,
        environment: provider === 'infisical' && !switching ? (this.form.environment || '') : '',
      });
    },
  },
};
</script>
