<template>
  <div data-testid="secretStorage-infisical">
    <v-alert variant="tonal" type="info" density="compact">
      {{ $t('infisicalPamNotice') }}
    </v-alert>

    <v-text-field
      v-model="clientIdModel"
      :label="$t('infisicalPamClientId')"
      :rules="[(v) => !!v || $t('infisicalPamClientIdRequired')]"
      :disabled="formSaving"
      data-testid="secretStorage-infisicalClientId"
      variant="outlined"
      density="compact"
    ></v-text-field>

    <SecretSourceToggle
      v-model="secretStorageModel"
      :label="$t('infisicalPamClientSecret')"
      :disabled="formSaving"
    />

    <v-text-field
      v-if="secretStorage === 'database'"
      class="TextInput TextInput--no-legend masked-secret-input"
      v-model="secretModel"
      :label="$t('infisicalPamClientSecret')"
      :disabled="formSaving"
      :rules="[(v) => !!v || !isNew || $t('infisicalPamClientSecretRequired')]"
      data-testid="secretStorage-infisicalClientSecret"
      variant="outlined"
      density="compact"
      append-icon="mdi-lock"
    ></v-text-field>

    <v-text-field
      v-else
      class="TextInput TextInput--no-legend"
      v-model="secretModel"
      :label="secretStorage === 'env' ? $t('Env var name') : $t('Path to the file')"
      :disabled="formSaving"
      :rules="[(v) => !!v || !isNew || $t('envvar_required')]"
      data-testid="secretStorage-infisicalClientSecretSource"
      variant="outlined"
      density="compact"
    ></v-text-field>

    <v-text-field
      v-model="organizationSlugModel"
      :label="$t('infisicalPamOrganizationSlug')"
      :disabled="formSaving"
      data-testid="secretStorage-infisicalOrganization"
      variant="outlined"
      density="compact"
    ></v-text-field>

    <v-text-field
      v-model="sessionDurationModel"
      :label="$t('infisicalPamSessionDuration')"
      :rules="[durationRule]"
      placeholder="1h"
      :disabled="formSaving"
      data-testid="secretStorage-infisicalSessionDuration"
      variant="outlined"
      density="compact"
    ></v-text-field>

    <v-textarea
      v-model="caCertificateModel"
      :label="$t('infisicalPamCACertificate')"
      :disabled="formSaving"
      data-testid="secretStorage-infisicalCACertificate"
      rows="3"
      variant="outlined"
      density="compact"
    ></v-textarea>

    <div class="d-flex align-center mb-2">
      <div class="text-body-large">{{ $t('infisicalPamRoutes') }}</div>
      <v-spacer />
      <v-btn
        variant="text"
        color="primary"
        :disabled="formSaving"
        data-testid="secretStorage-infisicalAddRoute"
        @click="addRoute"
      >
        <v-icon start>mdi-plus</v-icon>
        {{ $t('infisicalPamAddRoute') }}
      </v-btn>
    </div>

    <div
      v-for="(route, index) in routeList"
      :key="index"
      class="d-flex align-start"
      data-testid="secretStorage-infisicalRoute"
    >
      <v-text-field
        :model-value="route.host"
        :label="$t('infisicalPamRouteHost')"
        placeholder="web-1.example.com"
        :rules="[(v) => hostPattern.test(v || '') || $t('infisicalPamRouteHostInvalid')]"
        :disabled="formSaving"
        class="mr-3"
        variant="outlined"
        density="compact"
        @update:model-value="updateRoute(index, 'host', $event)"
      ></v-text-field>
      <v-text-field
        :model-value="route.account"
        :label="$t('infisicalPamRouteAccount')"
        placeholder="servers/web-1"
        :rules="[(v) => accountPattern.test(v || '') || $t('infisicalPamRouteAccountInvalid')]"
        :disabled="formSaving"
        variant="outlined"
        density="compact"
        @update:model-value="updateRoute(index, 'account', $event)"
      ></v-text-field>
      <v-btn
        icon
        class="ml-1 mt-1"
        :disabled="formSaving"
        :aria-label="$t('infisicalPamRemoveRoute')"
        @click="removeRoute(index)"
      >
        <v-icon>mdi-delete</v-icon>
      </v-btn>
    </div>

    <v-alert v-if="routeList.length === 0" variant="tonal" density="compact" type="warning">
      {{ $t('infisicalPamNoRoutes') }}
    </v-alert>
  </div>
</template>

<script>
import SecretSourceToggle from '@/components/SecretSourceToggle.vue';

// Mirrors the server's host and account validation so input errors show
// before saving; the server remains authoritative.
const HOST_PATTERN = /^[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?(\.[A-Za-z0-9_]([A-Za-z0-9_-]*[A-Za-z0-9_])?)*$/;
const ACCOUNT_PATTERN = /^[A-Za-z0-9][A-Za-z0-9 _.-]*(\/[A-Za-z0-9][A-Za-z0-9 _.-]*)+$/;
const DURATION_PATTERN = /^([0-9]+h)?([0-9]+m)?([0-9]+s)?$/;

export default {
  emits: [
    'update:caCertificate', 'update:clientId', 'update:organizationSlug', 'update:routes',
    'update:secret', 'update:secretStorage', 'update:sessionDuration',
  ],
  components: { SecretSourceToggle },
  props: {
    clientId: String,
    organizationSlug: String,
    sessionDuration: String,
    caCertificate: String,
    routes: Array,
    secret: String,
    secretStorage: String,
    isNew: Boolean,
    formSaving: Boolean,
  },
  data() {
    return { hostPattern: HOST_PATTERN, accountPattern: ACCOUNT_PATTERN };
  },
  computed: {
    routeList() {
      return this.routes || [];
    },
    clientIdModel: {
      get() { return this.clientId; },
      set(value) { this.$emit('update:clientId', value); },
    },
    organizationSlugModel: {
      get() { return this.organizationSlug; },
      set(value) { this.$emit('update:organizationSlug', value); },
    },
    sessionDurationModel: {
      get() { return this.sessionDuration; },
      set(value) { this.$emit('update:sessionDuration', value); },
    },
    caCertificateModel: {
      get() { return this.caCertificate; },
      set(value) { this.$emit('update:caCertificate', value); },
    },
    secretModel: {
      get() { return this.secret; },
      set(value) { this.$emit('update:secret', value); },
    },
    secretStorageModel: {
      get() { return this.secretStorage; },
      set(value) { this.$emit('update:secretStorage', value); },
    },
  },
  methods: {
    durationRule(value) {
      return !value || DURATION_PATTERN.test(value) || this.$t('infisicalPamSessionDurationInvalid');
    },
    addRoute() {
      this.$emit('update:routes', [...this.routeList, { host: '', account: '' }]);
    },
    removeRoute(index) {
      this.$emit('update:routes', this.routeList.filter((_, position) => position !== index));
    },
    updateRoute(index, field, value) {
      this.$emit(
        'update:routes',
        this.routeList.map((route, position) => (
          position === index ? { ...route, [field]: value } : route
        )),
      );
    },
  },
};
</script>
