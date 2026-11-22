import { Link } from "react-router";
import { IconStep } from "../components/icons.tsx";
import { StatusSections, useStatus } from "../components/StatusRow.tsx";
import { PageHeader, Segmented } from "../components/ui.tsx";
import { THEMES, useTheme } from "../lib/theme.ts";

export const MORE_DESTINATIONS = [
  { to: "/projects", label: "Projects" },
  { to: "/snippets", label: "Snippets" },
  { to: "/settings", label: "Settings" },
] as const;

const THEME_OPTIONS = THEMES.map((theme) => ({ value: theme, label: theme }));

export function MorePage() {
  const { theme, setTheme } = useTheme();
  const { row, details } = useStatus();

  return (
    <>
      <PageHeader title="More" />
      <div className="flex max-w-lg flex-col gap-5">
        <nav className="flex flex-col border border-line" aria-label="More destinations">
          {MORE_DESTINATIONS.map(({ to, label }) => (
            <Link
              key={to}
              to={to}
              className="flex min-h-11 items-center justify-between gap-3 border-b border-line px-3 py-2.5 text-body last:border-b-0 hover:bg-panel focus-visible:z-10"
            >
              {label}
              <span className="text-muted"><IconStep /></span>
            </Link>
          ))}
        </nav>
        <div className="flex flex-col border border-line" aria-label="Connections" role="group">
          <StatusSections row={row} details={details} touch />
        </div>
        <div className="flex flex-col items-start gap-2">
          <h2 className="label-caps">Theme</h2>
          <Segmented label="Theme" options={THEME_OPTIONS} value={theme} onChange={setTheme} fill />
        </div>
      </div>
    </>
  );
}
