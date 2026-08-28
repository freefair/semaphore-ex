import { getErrorMessage } from '@/lib/error';

const enhancedMethods = {
  authenticationErrorMessage(error) {
    const code = error.response?.data?.error;
    if (code === 'LDAP_PROVIDER_UNAVAILABLE') return this.$t('ldapProviderUnavailable');
    if (code === 'LDAP_THROTTLED') return this.$t('ldapLoginThrottled');
    if (code === 'LDAP_IDENTITY_COLLISION') return this.$t('ldapIdentityCollision');
    if (code === 'LDAP_DISABLED') return this.$t('ldapProviderDisabled');
    return getErrorMessage(error);
  },
};

export default enhancedMethods;
