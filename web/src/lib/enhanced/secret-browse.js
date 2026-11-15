import axios from 'axios';

// Loaders for SecretLocationPicker. Each returns a function that lists one
// level of a secret store for a location; responses carry names and
// identifiers only, never values.

function clean(location) {
  return Object.fromEntries(Object.entries(location || {}).filter(([, value]) => value));
}

export function providerLoader(providerId) {
  return (location) => axios
    .get(`/api/global-credential-providers/${encodeURIComponent(providerId)}/browse`, { params: clean(location) })
    .then((response) => response.data);
}

export function draftLoader(draft) {
  return (location) => axios
    .post('/api/global-secret-storage-drafts/browse', { storage: draft(), location: clean(location) })
    .then((response) => response.data);
}

export function projectStorageLoader(projectId, storageId) {
  return (location) => axios
    .get(`/api/project/${projectId}/secret_storages/${storageId}/browse`, { params: clean(location) })
    .then((response) => response.data);
}

// browseError turns a failed request into the message the server chose and
// its reason, so the picker can offer manual entry where listing is denied.
export function browseError(error) {
  const data = (error && error.response && error.response.data) || {};
  return {
    message: data.error || 'The store could not be read.',
    reason: data.reason || 'unavailable',
  };
}

// locationLabel names a picked location for people: names where known,
// identifiers otherwise.
export function locationLabel(parts) {
  return parts.filter((part) => part).join(' / ');
}
