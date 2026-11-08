import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { useT3Status, useTasks } from "../api/queries.ts";
import { THEMES, useTheme, type Theme } from "../lib/theme.ts";
import { CommandPalette } from "./CommandPalette.tsx";
import {
  IconChains,
  IconHistory,
  IconMoon,
  IconMore,
  IconPlus,
  IconProjects,
  IconSearch,
  IconStage,
  IconSun,
  IconThemeSystem,
} from "./icons.tsx";
import { NewTaskProvider, useNewTask } from "./NewTask.tsx";
import { Button, Chip, Drawer, Kbd, Segmented, cx } from "./ui.tsx";

const NAV = [
  { to: "/", label: "Dashboard" },
  { to: "/projects", label: "Projects" },
  { to: "/chains", label: "Chains" },
  { to: "/history", label: "History" },
  { to: "/templates", label: "Templates" },
  { to: "/settings", label: "Settings" },
] as const;

const TABS = [
  { to: "/", label: "Stage", icon: <IconStage /> },
  { to: "/projects", label: "Projects", icon: <IconProjects /> },
  { to: "/chains", label: "Chains", icon: <IconChains /> },
  { to: "/history", label: "History", icon: <IconHistory /> },
] as const;

const THEME_ICON: Record<Theme, ReactNode> = {
  system: <IconThemeSystem />,
  light: <IconSun />,
  dark: <IconMoon />,
};

function Mark() {
  return (
    <Link to="/" className="flex shrink-0 items-center gap-2 font-bold tracking-[-0.02em] whitespace-nowrap">
      <span className="size-2.5 bg-accent" />
      t3-stage
    </Link>
  );
}

function ConnectionChip({ compact = false }: { compact?: boolean }) {
  const status = useT3Status();
  const connected = status.data?.connected === true;
  const label = status.isPending ? "checking T3" : connected ? "T3 connected" : "T3 offline";
  return (
    <Link to="/settings" title={label} className="shrink-0">
      <Chip dot tone={status.isPending ? "idle" : connected ? "ok" : "bad"}>
        {compact ? "T3" : label}
      </Chip>
    </Link>
  );
}

function ThemeButton() {
  const { theme, cycle } = useTheme();
  return (
    <Button variant="secondary" onClick={cycle} title={`Theme: ${theme} (click to change)`} aria-label={`Theme: ${theme}`} className="w-8 px-0">
      {THEME_ICON[theme]}
    </Button>
  );
}

function NeedsCount({ n }: { n: number }) {
  if (n === 0) return null;
  return (
    <span title="Tasks needing input" className="chip tone-need px-1.5 text-[11px] leading-4">
      {n}
    </span>
  );
}

function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}

function Shell() {
  const needsInput = useTasks({ state: ["needs_input"] }).data?.tasks.length ?? 0;
  const newTask = useNewTask();
  const { theme, setTheme } = useTheme();
  const [palette, setPalette] = useState(false);
  const [more, setMore] = useState(false);
  const location = useLocation();
  const closePalette = useCallback(() => setPalette(false), []);
  const openNewTask = useCallback(() => newTask(), [newTask]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
        return;
      }
      const modalOpen = document.querySelector('[aria-modal="true"]') !== null;
      if (e.key.toLowerCase() === "n" && !e.ctrlKey && !e.metaKey && !e.altKey && !isTyping(e.target) && !modalOpen) {
        e.preventDefault();
        newTask();
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [newTask]);

  const moreActive = ["/templates", "/settings"].some((p) => location.pathname.startsWith(p));

  return (
    <div className="min-h-screen bg-bg text-fg">
      {/* Laptop top bar */}
      <header className="sticky top-0 z-40 hidden h-12 items-stretch gap-4 border-b border-line bg-bg px-6 md:flex">
        <div className="flex items-center">
          <Mark />
        </div>
        <nav className="flex items-stretch overflow-x-auto" aria-label="Main">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) =>
                cx(
                  "flex shrink-0 items-center gap-1.5 border-b-2 px-3 text-[13px] transition-colors duration-150",
                  isActive ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg",
                )
              }
            >
              {item.label}
              {item.to === "/" ? <NeedsCount n={needsInput} /> : null}
            </NavLink>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-2">
          <button
            type="button"
            onClick={() => setPalette(true)}
            className="flex h-8 items-center gap-3 border border-line-strong bg-surface px-2.5 text-[12.5px] whitespace-nowrap text-muted transition-colors duration-150 hover:text-fg"
          >
            <IconSearch size={14} />
            <span className="hidden lg:inline">Search tasks, threads</span>
            <Kbd>Ctrl K</Kbd>
          </button>
          <ConnectionChip />
          <ThemeButton />
          <Button variant="primary" onClick={() => newTask()}>
            <IconPlus size={14} />
            New task
          </Button>
        </div>
      </header>

      {/* Phone top bar */}
      <header className="sticky top-0 z-40 flex h-12 items-center gap-2 border-b border-line bg-bg px-3.5 md:hidden">
        <Mark />
        <div className="ml-auto flex items-center gap-2">
          <ConnectionChip compact />
          <Button variant="secondary" small onClick={() => setPalette(true)} aria-label="Search" className="h-8 w-8 px-0">
            <IconSearch size={15} />
          </Button>
          <Button variant="primary" small onClick={() => newTask()} className="h-8">
            <IconPlus size={13} />
            Task
          </Button>
        </div>
      </header>

      <main className="mx-auto w-full max-w-[1240px] px-3 pt-4 pb-24 md:px-6 md:pt-6 md:pb-10">
        <Outlet />
      </main>

      {/* Phone bottom tabs */}
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-40 grid h-14 grid-cols-5 border-t border-line bg-bg pb-[env(safe-area-inset-bottom)] md:hidden"
      >
        {TABS.map((t) => (
          <NavLink
            key={t.to}
            to={t.to}
            end={t.to === "/"}
            className={({ isActive }) =>
              cx(
                "relative -mt-px flex flex-col items-center justify-center gap-1 border-t-2 text-[10.5px]",
                isActive ? "border-accent text-fg" : "border-transparent text-muted",
              )
            }
          >
            {t.icon}
            {t.label}
            {t.to === "/" && needsInput > 0 ? (
              <span className="absolute top-1.5 left-[calc(50%+6px)] size-2 bg-need" aria-label={`${needsInput} need input`} />
            ) : null}
          </NavLink>
        ))}
        <button
          type="button"
          onClick={() => setMore(true)}
          className={cx(
            "-mt-px flex flex-col items-center justify-center gap-1 border-t-2 text-[10.5px]",
            moreActive ? "border-accent text-fg" : "border-transparent text-muted",
          )}
        >
          <IconMore />
          More
        </button>
      </nav>

      {more ? (
        <Drawer title="More" onClose={() => setMore(false)}>
          <div className="flex flex-col gap-5">
            <nav className="flex flex-col border border-line" aria-label="More">
              {NAV.slice(4).map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={() => setMore(false)}
                  className={({ isActive }) =>
                    cx("border-b border-line px-3 py-3 text-[14px] last:border-b-0", isActive && "bg-panel font-medium")
                  }
                >
                  {item.label}
                </NavLink>
              ))}
            </nav>
            <div className="flex flex-col gap-2">
              <span className="label-caps">Theme</span>
              <Segmented
                label="Theme"
                options={THEMES.map((t) => ({ value: t, label: t }))}
                value={theme}
                onChange={setTheme}
              />
            </div>
          </div>
        </Drawer>
      ) : null}

      {palette ? <CommandPalette onClose={closePalette} onNewTask={openNewTask} /> : null}
    </div>
  );
}

export function Layout() {
  return (
    <NewTaskProvider>
      <Shell />
    </NewTaskProvider>
  );
}
