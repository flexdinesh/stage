import type { HostState, PreferenceStorage } from "@stage/app/platform";

export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** The webview's localStorage (kept per app identity by the OS webview). Unavailable storage reads as null. */
export function desktopStorage(current: () => HostState, store: () => StorageLike = () => localStorage): PreferenceStorage {
  let accountScope = "";
  const session = new Map<string, string>();
  const scope = (key: string) => {
    const state = current();
    const target = JSON.stringify([state.target.id, state.target.url]);
    const account = state.server?.sharedAccess === true ? JSON.stringify([target, state.accountEpoch]) : "";
    if (accountScope !== account) {
      accountScope = account;
      session.clear();
    }
    if (key === "stage-theme") return { key, shared: false };
    return state.server?.sharedAccess === true
      ? { key, shared: true }
      : { key: state.target.kind === "local" ? key : `${target}:${key}`, shared: false };
  };
  return {
    get(key) {
      const storage = scope(key);
      if (storage.shared) return session.get(storage.key) ?? null;
      try {
        return store().getItem(storage.key);
      } catch {
        return null;
      }
    },
    set(key, value) {
      const storage = scope(key);
      if (storage.shared) {
        if (value === null) session.delete(storage.key);
        else session.set(storage.key, value);
        return;
      }
      try {
        if (value === null) store().removeItem(storage.key);
        else store().setItem(storage.key, value);
      } catch {
        // Storage unavailable: the preference applies for this run only.
      }
    },
  };
}
