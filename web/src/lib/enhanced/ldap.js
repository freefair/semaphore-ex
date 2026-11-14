// Shared LDAP UI vocabulary: presets, readiness stages and messages.
import axios from 'axios';

export function emptyLdapProvider() {
  return {
    id: '',
    display_name: '',
    state: 'disabled',
    server_url: '',
    tls_mode: 'ldaps',
    trust_mode: 'system',
    ca_pem: '',
    bind_dn: '',
    bind_password_configured: false,
    search_base_dn: '',
    user_filter: '(uid={{username}})',
    identity_attribute: 'entryUUID',
    username_attribute: 'uid',
    name_attribute: 'cn',
    email_attribute: 'mail',
    group_search_base_dn: '',
    group_user_filter: '(objectClass=person)',
    group_filter: '(objectClass=groupOfNames)',
    group_identity_attribute: 'entryUUID',
    group_member_attribute: 'member',
    group_name_attribute: 'cn',
    group_max_depth: 4,
    selected_user_ids: [],
    eligible_user_ids: [],
    readiness: { status: 'untested' },
  };
}

export const LDAP_PRESETS = {
  active_directory: {
    user_filter: '(&(objectClass=user)(sAMAccountName={{username}}))',
    identity_attribute: 'objectGUID',
    username_attribute: 'sAMAccountName',
    name_attribute: 'displayName',
    email_attribute: 'mail',
    group_user_filter: '(&(objectClass=user)(objectCategory=person))',
    group_filter: '(objectClass=group)',
    group_identity_attribute: 'objectGUID',
    group_member_attribute: 'member',
    group_name_attribute: 'cn',
  },
  openldap: {
    user_filter: '(uid={{username}})',
    identity_attribute: 'entryUUID',
    username_attribute: 'uid',
    name_attribute: 'cn',
    email_attribute: 'mail',
    group_user_filter: '(objectClass=person)',
    group_filter: '(objectClass=groupOfNames)',
    group_identity_attribute: 'entryUUID',
    group_member_attribute: 'member',
    group_name_attribute: 'cn',
  },
};

export function applyLdapPreset(form, preset) {
  return { ...form, ...LDAP_PRESETS[preset] };
}

// The stages a directory test passes in order. A failure code names the stage
// that stopped the test; later stages were not reached.
export const LDAP_TEST_STAGES = [
  { key: 'connection', label: 'ldapStageConnection' },
  { key: 'search', label: 'ldapStageSearch' },
  { key: 'bind', label: 'ldapStageBind' },
  { key: 'recovery', label: 'ldapStageRecovery' },
];

const FAILED_STAGE = {
  invalid_configuration: 'connection',
  connection_failed: 'connection',
  service_bind_failed: 'connection',
  invalid_filter: 'search',
  search_failed: 'search',
  referral_rejected: 'search',
  duplicate_identity: 'search',
  invalid_identity: 'search',
  identity_attribute_missing: 'search',
  identity_not_uuid: 'search',
  required_attribute_missing: 'search',
  invalid_credentials: 'bind',
  local_recovery_failed: 'recovery',
};

// A stored failure keeps only its code, so stages before the failed one count
// as passed (the test could not have reached the failure otherwise).
export function ldapStageStates(readiness) {
  const status = readiness?.status;
  const failedIndex = status === 'failed'
    ? LDAP_TEST_STAGES.findIndex((stage) => stage.key === FAILED_STAGE[readiness.code]) : -1;
  return LDAP_TEST_STAGES.map((stage, index) => {
    let state = 'pending';
    if (status === 'ready' || readiness?.[stage.key]) state = 'passed';
    else if (failedIndex >= 0 && index < failedIndex) state = 'passed';
    else if (index === failedIndex) state = 'failed';
    else if (failedIndex >= 0) state = 'not_reached';
    return { ...stage, state };
  });
}

export const LDAP_READINESS_MESSAGES = {
  untested: 'ldapReadinessUntested',
  configuration_changed: 'ldapReadinessConfigurationChanged',
  ready: 'ldapReadinessReady',
  invalid_configuration: 'ldapReadinessInvalidConfiguration',
  connection_failed: 'ldapReadinessConnectionFailed',
  service_bind_failed: 'ldapReadinessServiceBindFailed',
  invalid_filter: 'ldapReadinessInvalidFilter',
  search_failed: 'ldapReadinessSearchFailed',
  referral_rejected: 'ldapReadinessReferral',
  duplicate_identity: 'ldapReadinessDuplicateIdentity',
  invalid_identity: 'ldapReadinessInvalidIdentity',
  identity_attribute_missing: 'ldapReadinessIdentityAttributeMissing',
  identity_not_uuid: 'ldapReadinessIdentityNotUuid',
  required_attribute_missing: 'ldapReadinessRequiredAttributeMissing',
  invalid_credentials: 'ldapReadinessInvalidCredentials',
  local_recovery_failed: 'ldapReadinessLocalRecoveryFailed',
};

export const LDAP_ERROR_MESSAGES = {
  LDAP_ADMIN_RECOVERY_NOT_READY: 'ldapErrorRecoveryNotReady',
  LDAP_PROVIDER_UNAVAILABLE: 'ldapErrorProviderUnavailable',
  LDAP_REFERRAL: 'ldapErrorReferral',
  LDAP_DUPLICATE_IDENTITY: 'ldapErrorDuplicateIdentity',
  LDAP_INVALID_CREDENTIALS: 'ldapErrorInvalidCredentials',
  LDAP_RECONFIGURATION_REQUIRES_INACTIVE: 'ldapErrorReconfigurationRequiresInactive',
  LDAP_NO_GROUP_MAPPING: 'ldapErrorNoGroupMapping',
  LDAP_GROUP_PREVIEW_STALE: 'ldapErrorPreviewStale',
  LDAP_GROUP_MAPPING_COLLISION: 'ldapErrorMappingCollision',
  LDAP_GROUP_PROTECTED_ADMINISTRATOR: 'ldapErrorProtectedAdministrator',
  LDAP_GROUP_UNRESOLVED: 'ldapErrorUnresolved',
  LDAP_THROTTLED: 'ldapErrorThrottled',
  LDAP_PROVIDER_NOT_FOUND: 'ldapErrorProviderNotFound',
};

// Returns a translation key with the raw code as fallback text, so validation
// messages from the server (plain sentences) are shown as they are.
export function ldapErrorText(error, t) {
  const code = error?.response?.data?.error;
  if (code && LDAP_ERROR_MESSAGES[code]) return t(LDAP_ERROR_MESSAGES[code]);
  return code || error?.message || t('ldapErrorUnknown');
}

export const READINESS_VALIDITY_MINUTES = 15;

export function readinessExpiry(readiness) {
  if (!readiness || readiness.status !== 'ready' || !readiness.checked_at) return null;
  const checked = new Date(readiness.checked_at);
  if (Number.isNaN(checked.getTime())) return null;
  return new Date(checked.getTime() + READINESS_VALIDITY_MINUTES * 60 * 1000);
}

export function mappingIdFromName(name, existingIds = []) {
  const base = (name || 'group').toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48) || 'group';
  let candidate = base;
  let suffix = 2;
  while (existingIds.includes(candidate)) {
    candidate = `${base}-${suffix}`;
    suffix += 1;
  }
  return candidate;
}

export async function loadLdapProviders() {
  const response = await axios.get('/api/capabilities/ldap');
  return response.data || [];
}
