/**
 * Reads a bgmcat.* localStorage entry, transparently migrating from the
 * legacy anime-rss* key on first read so existing sessions, themes, and
 * watch history survive the project rename without data loss.
 */
export function readStorageItem(key: string, legacyKey?: string): string | null {
  try {
    const current = window.localStorage.getItem(key);
    if (current !== null) {
      return current;
    }
    if (legacyKey) {
      const legacy = window.localStorage.getItem(legacyKey);
      if (legacy !== null) {
        try {
          window.localStorage.setItem(key, legacy);
          window.localStorage.removeItem(legacyKey);
        } catch {
          // ignore quota errors while still returning the migrated value
        }
        return legacy;
      }
    }
  } catch {
    // ignore storage restriction errors
  }
  return null;
}
