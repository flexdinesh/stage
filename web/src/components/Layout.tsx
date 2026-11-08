import { NavLink, Outlet } from "react-router";
import { useT3Status, useTasks } from "../api/queries.ts";
import { cx } from "./ui.tsx";

const NAV = [
  { to: "/", label: "Dashboard" },
  { to: "/projects", label: "Projects" },
  { to: "/chains", label: "Chains" },
  { to: "/history", label: "History" },
  { to: "/templates", label: "Templates" },
  { to: "/settings", label: "Settings" },
] as const;

function ConnectionDot() {
  const status = useT3Status();
  const connected = status.data?.connected === true;
  const label = status.isPending ? "checking T3" : connected ? "T3 connected" : "T3 not connected";
  return (
    <NavLink to="/settings" title={label} className="ml-auto flex items-center gap-1.5 text-xs text-neutral-500">
      <span className={cx("size-2 rounded-full", status.isPending ? "bg-neutral-400" : connected ? "bg-green-500" : "bg-red-500")} />
      <span className="hidden sm:inline">{label}</span>
    </NavLink>
  );
}

export function Layout() {
  const needsInput = useTasks({ state: ["needs_input"] }).data?.tasks.length ?? 0;
  return (
    <div className="min-h-screen bg-neutral-50 text-neutral-900 dark:bg-neutral-950 dark:text-neutral-100">
      <header className="sticky top-0 z-40 border-b border-neutral-200 bg-white/90 backdrop-blur dark:border-neutral-800 dark:bg-neutral-900/90">
        <nav className="mx-auto flex max-w-6xl items-center gap-1 overflow-x-auto px-4 py-2">
          <span className="mr-3 shrink-0 font-semibold">t3-stage</span>
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) =>
                cx(
                  "flex shrink-0 items-center gap-1.5 rounded px-3 py-1.5 text-sm",
                  isActive
                    ? "bg-neutral-900 text-white dark:bg-neutral-100 dark:text-neutral-900"
                    : "text-neutral-600 hover:bg-neutral-100 dark:text-neutral-300 dark:hover:bg-neutral-800",
                )
              }
            >
              {item.label}
              {item.to === "/" && needsInput > 0 ? (
                <span className="rounded-full bg-amber-500 px-1.5 text-xs font-semibold text-white" title="Tasks needing input">
                  {needsInput}
                </span>
              ) : null}
            </NavLink>
          ))}
          <ConnectionDot />
        </nav>
      </header>
      <main className="mx-auto max-w-6xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}
