import type { PreferenceStorage } from "@stage/app/platform";

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** The webview's localStorage (kept per app identity by the OS webview). Unavailable storage reads as null. */
export function desktopStorage(store: () => StorageLike = () => localStorage): PreferenceStorage {
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
        // Storage unavailable: the preference applies for this run only.
      }
    },
  };
}
