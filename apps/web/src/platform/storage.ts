import type { PreferenceStorage } from "@stage/app/platform";

/** localStorage, or whatever throws in its place (private mode, blocked site data). */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** Per-browser preferences. Unavailable storage reads as null and drops writes. */
export function browserStorage(store: () => StorageLike = () => localStorage): PreferenceStorage {
  return {
    get(key) {
      try {
        return store().getItem(key);
      } catch {
        return null;
      }
    },
    set(key, value) {
      try {
        if (value === null) store().removeItem(key);
        else store().setItem(key, value);
      } catch {
        // Storage unavailable: the preference applies for this page view only.
      }
    },
  };
}
