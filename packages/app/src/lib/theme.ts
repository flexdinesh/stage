import { useCallback, useSyncExternalStore } from "react";
import { usePlatform } from "../platform/context.tsx";
import type { PreferenceStorage } from "../platform/index.ts";

export const THEMES = ["system", "light", "dark"] as const;
export type Theme = (typeof THEMES)[number];

// Read before first paint by the host page too (web/index.html), so keep the key stable.
const KEY = "stage-theme";
const listeners = new Set<() => void>();

function read(storage: PreferenceStorage): Theme {
  const v = storage.get(KEY);
  return THEMES.find((t) => t === v) ?? "system";
}

/** Applies a theme to <html>: "system" removes the override so prefers-color-scheme wins. */
export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

/** The theme is document-wide, so one store per page; read from storage on first use. */
let current: Theme | null = null;

function setTheme(storage: PreferenceStorage, theme: Theme): void {
  // Storage unavailable (private mode): theme still applies for this page view.
  storage.set(KEY, theme === "system" ? null : theme);
  applyTheme(theme);
  current = theme;
  for (const l of listeners) l();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Current theme preference plus a setter and a system → light → dark cycler. */
export function useTheme(): { theme: Theme; setTheme: (t: Theme) => void; cycle: () => void } {
  const { storage } = usePlatform();
  const theme = useSyncExternalStore(subscribe, () => (current ??= read(storage)));
  const set = useCallback((t: Theme) => setTheme(storage, t), [storage]);
  const cycle = useCallback(() => {
    const i = THEMES.indexOf(current ?? read(storage));
    setTheme(storage, THEMES[(i + 1) % THEMES.length] ?? "system");
  }, [storage]);
  return { theme, setTheme: set, cycle };
}
