// Navigation destinations shared by the shell (components/Layout.tsx) and the pages that list them.

/** Pages behind the phone tab bar's More tab; More stays highlighted on each. */
export const MORE_DESTINATIONS = [
  { to: "/projects", label: "Projects" },
  { to: "/snippets", label: "Snippets" },
  { to: "/settings", label: "Settings" },
] as const;
