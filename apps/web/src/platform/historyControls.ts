/**
 * Whether the browser shows its own back and forward: a normal tab and a minimal-ui app window do; an installed
 * standalone app window (this app's manifest) does not.
 */
export function browserHistoryControls(matches: (query: string) => boolean = (q) => window.matchMedia(q).matches): boolean {
  return matches("(display-mode: browser)") || matches("(display-mode: minimal-ui)");
}
