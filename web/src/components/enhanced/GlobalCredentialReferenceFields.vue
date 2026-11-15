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
    <template v-if="isInfisical">
      <v-alert
        type="info"
        variant="tonal"
        density="compact"
        class="mb-4"
        data-testid="credential-reference-scopes"
      >
        <template v-if="scopes.length === 0">
          This connection has no secret scopes; an administrator adds them under
          Global secret storages.
        </template>
        <template v-else>
          Allowed: {{ scopes.map(scopeLabel).join(', ') }}
        </template>
      </v-alert>
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
        hint="Relative to the root folder; empty reads from the root."
        persistent-hint
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
      <v-text-field
        :model-value="form.version"
        label="Version (optional)"
        type="number"
        min="0"
        hint="0 reads the current version."
        persistent-hint
        data-testid="credential-reference-infisical-version"
        @update:model-value="update('version', Number($event))"
      />
    </template>
    <template v-else>
      <v-text-field
        :model-value="form.mount"
        label="Mount"
        @update:model-value="update('mount', $event)"
      />
      <v-text-field
        :model-value="form.path"
        label="Secret path"
        @update:model-value="update('path', $event)"
      />
      <v-text-field
        :model-value="form.version"
        label="Secret version"
        type="number"
        min="1"
        @update:model-value="update('version', Number($event))"
      />
      <v-text-field
        :model-value="form.field"
        label="Field"
        @update:model-value="update('field', $event)"
      />
    </template>
  </div>
</template>

<script>
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
  emits: ['update:form'],
  props: {
    form: { type: Object, required: true },
    // Value-free provider identities from GET /api/global-credential-providers.
    providers: { type: Array, default: () => [] },
  },
  computed: {
    isInfisical() {
      return this.form.provider === 'infisical';
    },
    scopes() {
      const provider = this.providers.find((p) => p.provider === this.form.provider
        && p.provider_id === this.form.providerId);
      return (provider && provider.scopes) || [];
    },
    inScope() {
      return referenceInScope(this.form, this.scopes);
    },
    sourceKey() {
      return this.form.providerId ? `${this.form.provider}|${this.form.providerId}` : null;
    },
    sourceItems() {
      return this.providers.map((provider) => ({
        title: `${PROVIDER_LABELS[provider.provider] || provider.provider}: ${provider.name}`,
        value: `${provider.provider}|${provider.provider_id}`,
      }));
    },
  },
  methods: {
    scopeLabel(scope) {
      return `${scope.project_id} / ${scope.environment}${scope.path ? ` / ${scope.path}` : ''}`;
    },
    update(key, value) {
      this.$emit('update:form', { ...this.form, [key]: value });
    },
    selectSource(value) {
      const [provider, providerId] = (value || '|').split('|');
      const switching = provider !== this.form.provider;
      this.$emit('update:form', {
        ...this.form,
        provider,
        providerId,
        // Vault/OpenBao pin a version; Infisical reads the current one by default.
        version: switching ? Number(provider !== 'infisical') : this.form.version,
        environment: provider === 'infisical' ? (this.form.environment || '') : '',
      });
    },
  },
};
</script>
