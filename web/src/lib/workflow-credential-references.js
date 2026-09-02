function positiveInteger(value) {
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null;
}

export function credentialReferenceKey(reference) {
  if (!reference || typeof reference !== 'object') return '';
  const accessKeyId = positiveInteger(reference.access_key_id);
  const globalCredentialId = positiveInteger(reference.global_credential_id);
  if (Boolean(accessKeyId) === Boolean(globalCredentialId)) return '';
  return accessKeyId ? `access_key:${accessKeyId}` : `global_credential:${globalCredentialId}`;
}

export function credentialReferenceFromKey(value) {
  if (Number.isSafeInteger(value) && value > 0) return { access_key_id: value };
  if (typeof value !== 'string') return null;
  const separator = value.indexOf(':');
  if (separator <= 0) return null;
  const kind = value.slice(0, separator);
  const id = positiveInteger(value.slice(separator + 1));
  if (!id) return null;
  if (kind === 'access_key') return { access_key_id: id };
  if (kind === 'global_credential') return { global_credential_id: id };
  return null;
}

export function credentialOptionItems(options) {
  return (options || []).map((option) => {
    const reference = {
      access_key_id: option.access_key_id,
      global_credential_id: option.global_credential_id,
    };
    const value = credentialReferenceKey(reference);
    return {
      value,
      reference: credentialReferenceFromKey(value),
      text: option.label || (option.global_credential_id
        ? `Global credential #${option.global_credential_id}`
        : `Credential #${option.access_key_id}`),
    };
  }).filter((option) => option.reference);
}
