import { Link } from "react-router";
import { moreDestinations } from "../components/destinations.ts";
import { useIsAdmin } from "../components/access/SessionContext.ts";
import { IconStep } from "../components/icons.tsx";
import { StatusSections, useStatus } from "../components/StatusRow.tsx";
import { PageHeader, Segmented, SubHeading, cx, rowButtonClass } from "../components/ui.tsx";
import { THEMES, useTheme } from "../lib/theme.ts";

const THEME_OPTIONS = THEMES.map((theme) => ({ value: theme, label: theme }));

export function MorePage() {
  const { theme, setTheme } = useTheme();
  const { row, details } = useStatus();
  const admin = useIsAdmin();

  return (
    <>
      <PageHeader title="More" />
      <div className="flex max-w-lg flex-col gap-5">
        <nav className="flex flex-col border border-line" aria-label="More destinations">
          {moreDestinations(admin).map(({ to, label }) => (
            <Link
              key={to}
              to={to}
              className={cx(rowButtonClass(), "justify-between border-b border-line text-body last:border-b-0 focus-visible:z-10")}
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
          <SubHeading title="Theme" />
          <Segmented label="Theme" options={THEME_OPTIONS} value={theme} onChange={setTheme} fill />
        </div>
      </div>
    </>
  );
}
