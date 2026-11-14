import { useCallback, useSyncExternalStore } from "react";

export const THEMES = ["system", "light", "dark"] as const;
export type Theme = (typeof THEMES)[number];

const KEY = "stage-theme";
const listeners = new Set<() => void>();

function read(): Theme {
  try {
    const v = localStorage.getItem(KEY);
    return THEMES.find((t) => t === v) ?? "system";
  } catch {
    return "system";
  }
}

/** Applies a theme to <html>: "system" removes the override so prefers-color-scheme wins. */
export function applyTheme(theme: Theme): void {
  const root = document.documentElement;
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
}

export function setTheme(theme: Theme): void {
  try {
    if (theme === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, theme);
  } catch {
    // Storage unavailable (private mode): theme still applies for this page view.
  }
  applyTheme(theme);
  current = theme;
  for (const l of listeners) l();
}

let current: Theme = read();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Current theme preference plus a setter and a system → light → dark cycler. */
export function useTheme(): { theme: Theme; setTheme: (t: Theme) => void; cycle: () => void } {
  const theme = useSyncExternalStore(subscribe, () => current);
  const cycle = useCallback(() => {
    const i = THEMES.indexOf(current);
    setTheme(THEMES[(i + 1) % THEMES.length] ?? "system");
  }, []);
  return { theme, setTheme, cycle };
}
