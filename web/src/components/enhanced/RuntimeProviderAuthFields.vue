<template>
  <div>
  <v-textarea
    v-model="caCertificateModel"
    label="Custom CA certificate (PEM, optional)"
    :disabled="formSaving"
    data-testid="secretStorage-vaultCACertificate"
    rows="3"
    outlined
    dense
  ></v-textarea>

  <v-text-field
    v-model="timeoutModel"
    label="Request timeout"
    hint="Go duration up to 30s, for example 5s"
    :disabled="formSaving"
    data-testid="secretStorage-vaultTimeout"
    outlined
    dense
  ></v-text-field>

  <v-select
    v-model="authMethodModel"
    label="Authentication method"
    :items="runtimeAuthMethods"
    item-value="value"
    item-text="text"
    :disabled="formSaving"
    data-testid="secretStorage-vaultAuthMethod"
    outlined
    dense
  ></v-select>

  <v-text-field
    v-if="authMethod !== 'token'"
    v-model="authMountModel"
    label="Authentication mount"
    :hint="
      authMethod === 'approle' ? 'approle by default' : 'kubernetes by default'
    "
    :disabled="formSaving"
    data-testid="secretStorage-vaultAuthMount"
    outlined
    dense
  ></v-text-field>

  <v-text-field
    v-if="authMethod === 'approle'"
    v-model="roleIdModel"
    label="AppRole role ID"
    :rules="[(v) => !!v || 'Role ID is required']"
    :disabled="formSaving"
    data-testid="secretStorage-vaultRoleId"
    outlined
    dense
  ></v-text-field>

  <v-text-field
    v-if="authMethod === 'kubernetes'"
    v-model="roleModel"
    label="Kubernetes role"
    :rules="[(v) => !!v || 'Kubernetes role is required']"
    :disabled="formSaving"
    data-testid="secretStorage-vaultKubernetesRole"
    outlined
    dense
  ></v-text-field>

  <SecretSourceToggle
    v-model="secretStorageModel"
    :label="runtimeCredentialLabel"
    :disabled="formSaving"
  />
  </div>
</template>

<script>
import SecretSourceToggle from '@/components/SecretSourceToggle.vue';

export default {
  components: { SecretSourceToggle },
  props: {
    caCertificate: String,
    timeout: String,
    authMethod: String,
    authMount: String,
    roleId: String,
    role: String,
    secretStorage: String,
    runtimeCredentialLabel: String,
    runtimeAuthMethods: Array,
    formSaving: Boolean,
  },
  computed: {
    caCertificateModel: {
      get() { return this.caCertificate; },
      set(value) { this.$emit('update:caCertificate', value); },
    },
    timeoutModel: {
      get() { return this.timeout; },
      set(value) { this.$emit('update:timeout', value); },
    },
    authMethodModel: {
      get() { return this.authMethod; },
      set(value) { this.$emit('update:authMethod', value); },
    },
    authMountModel: {
      get() { return this.authMount; },
      set(value) { this.$emit('update:authMount', value); },
    },
    roleIdModel: {
      get() { return this.roleId; },
      set(value) { this.$emit('update:roleId', value); },
    },
    roleModel: {
      get() { return this.role; },
      set(value) { this.$emit('update:role', value); },
    },
    secretStorageModel: {
      get() { return this.secretStorage; },
      set(value) { this.$emit('update:secretStorage', value); },
    },
  },
};
</script>
