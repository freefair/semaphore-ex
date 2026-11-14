// New repositories and inventories often need no credentials. Preselect the
// project's "None" key so the required picker is not a hurdle; the user can
// still choose a real key.
export function noneKeyId(keys) {
  const none = (keys || []).find((key) => key.type === 'none');
  return none ? none.id : null;
}

// Returns the key to preselect, or null when the field must stay as it is.
export function preselectedKeyId(item, keys, field, isNew) {
  if (!isNew || !item || item[field] != null) return null;
  return noneKeyId(keys);
}

export const preselectNoneKeyWatch = (field) => ({
  isLoaded: {
    immediate: true,
    handler(loaded) {
      if (!loaded) return;
      const id = preselectedKeyId(this.item, this.keys, field, this.isNew);
      if (id != null) this.item[field] = id;
    },
  },
});
