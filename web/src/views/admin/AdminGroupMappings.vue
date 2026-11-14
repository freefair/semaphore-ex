<template>
  <div data-testid="admin-group-mappings">
    <v-toolbar flat>
      <v-toolbar-title>{{ $t('adminGroupMappings') }}</v-toolbar-title>
    </v-toolbar>

    <div class="px-4 pb-8">
      <p class="mb-6">{{ $t('adminGroupMappingsIntro') }}</p>
      <LdapGroupMappingPanel />
      <v-divider class="my-8" />
      <OidcGroupMappingPanel />
    </div>
  </div>
</template>

<script>
import LdapGroupMappingPanel from '@/components/enhanced/ldap/LdapGroupMappingPanel.vue';
import OidcGroupMappingPanel from '@/components/OidcGroupMappingPanel.vue';

export default {
  components: { LdapGroupMappingPanel, OidcGroupMappingPanel },
};
</script>
