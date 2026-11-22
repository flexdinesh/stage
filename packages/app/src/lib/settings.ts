// Settings sub-navigation: one tab per area, each its own route. General is the index (/settings).

export type SettingsTab = "general" | "connections" | "notifications";

export const SETTINGS_PATH: Record<SettingsTab, string> = {
  general: "/settings",
  connections: "/settings/connections",
  notifications: "/settings/notifications",
};

export const SETTINGS_TABS: readonly { tab: SettingsTab; label: string; to: string }[] = [
  { tab: "general", label: "General", to: SETTINGS_PATH.general },
  { tab: "connections", label: "Connections", to: SETTINGS_PATH.connections },
  { tab: "notifications", label: "Notifications", to: SETTINGS_PATH.notifications },
];

/** The tab a Settings path shows; unknown subpaths fall back to General. */
export function settingsTab(pathname: string): SettingsTab {
  const rest = pathname.replace(/\/+$/, "");
  for (const t of SETTINGS_TABS) {
    if (t.tab !== "general" && (rest === t.to || rest.startsWith(`${t.to}/`))) return t.tab;
  }
  return "general";
}

/** Info's Version line from GET /api/health: "v0.9.4 · 65bac1c · api 7" (short commit; empty parts left out). */
export function versionLine(h: { version: string; commit: string; apiVersion: number }): string {
  return [h.version === "" ? null : h.version, h.commit === "" ? null : h.commit.slice(0, 7), `api ${h.apiVersion}`].filter((p) => p !== null).join(" · ");
}

/** Info's Running as line: the main machine, and how the desktop app's own server runs when known. */
export const runningAs = (runs: string | null): string => (runs === null ? "main" : `main · ${runs}`);

/** The tab's label for page meta and history ("Connections"). */
export const settingsTabLabel = (tab: SettingsTab): string => SETTINGS_TABS.find((t) => t.tab === tab)?.label ?? "General";
