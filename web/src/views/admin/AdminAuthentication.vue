<template>
  <div data-testid="admin-authentication">
    <v-toolbar flat>
      <v-toolbar-title>{{ $t('adminAuthentication') }}</v-toolbar-title>
    </v-toolbar>

    <div class="px-4 pb-8">
      <TwoFactorPolicyPanel />

      <v-divider class="my-8" />

      <section data-testid="admin-authentication-ldap">
        <h2 class="text-title-large font-weight-medium mb-1">{{ $t('adminDirectoryTitle') }}</h2>
        <p class="mb-4">{{ $t('adminDirectoryIntro') }}</p>
        <LdapProviderSetup />
      </section>

      <v-divider class="my-8" />

      <section data-testid="admin-authentication-oidc">
        <h2 class="text-title-large font-weight-medium mb-1">{{ $t('adminSsoTitle') }}</h2>
        <p class="mb-2">{{ $t('adminSsoIntro') }}</p>
        <p v-if="oidcProviders.length" data-testid="admin-oidc-providers">
          {{ $t('adminSsoConfigured') }}
          <v-chip v-for="provider in oidcProviders" :key="provider" size="small" class="ml-1">
            {{ provider }}
          </v-chip>
        </p>
        <p v-else class="text-medium-emphasis" data-testid="admin-oidc-none">
          {{ $t('adminSsoNone') }}
        </p>
      </section>
    </div>
  </div>
</template>

<script>
import axios from 'axios';
import TwoFactorPolicyPanel from '@/components/enhanced/TwoFactorPolicyPanel.vue';
import LdapProviderSetup from '@/components/enhanced/ldap/LdapProviderSetup.vue';

export default {
  components: { TwoFactorPolicyPanel, LdapProviderSetup },

  data() {
    return { oidcProviders: [] };
  },

  async created() {
    try {
      const info = (await axios.get('/api/admin/info')).data;
      this.oidcProviders = info.auth?.oidc_providers || [];
    } catch {
      this.oidcProviders = [];
    }
  },
};
</script>
