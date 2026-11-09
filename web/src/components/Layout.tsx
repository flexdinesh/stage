import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { useBadge, useT3Status } from "../api/queries.ts";
import { THEMES, useTheme, type Theme } from "../lib/theme.ts";
import { CommandPalette } from "./CommandPalette.tsx";
import {
  IconHistory,
  IconMoon,
  IconMore,
  IconPlus,
  IconProjects,
  IconSearch,
  IconStage,
  IconSun,
  IconSnippets,
  IconThemeSystem,
} from "./icons.tsx";
import { NewTaskProvider, useNewTask } from "./NewTask.tsx";
import { NotificationToasts, NotificationsBell, NotificationsDrawer, usePushRefresh, useSystemNotificationSync } from "./Notifications.tsx";
import { PullToReload } from "./PullToReload.tsx";
import { Button, Chip, Drawer, Kbd, Segmented, cx } from "./ui.tsx";

const NAV = [
  { to: "/", label: "Dashboard" },
  { to: "/projects", label: "Projects" },
  { to: "/sessions", label: "Sessions" },
  { to: "/snippets", label: "Snippets" },
  { to: "/settings", label: "Settings" },
] as const;

/** Phone tab bar icon size (iOS tab bars use ~22-25px glyphs). */
const TAB_ICON = 22;

const TABS = [
  { to: "/", label: "Stage", icon: <IconStage size={TAB_ICON} /> },
  { to: "/projects", label: "Projects", icon: <IconProjects size={TAB_ICON} /> },
  { to: "/sessions", label: "Sessions", icon: <IconHistory size={TAB_ICON} /> },
  { to: "/snippets", label: "Snippets", icon: <IconSnippets size={TAB_ICON} /> },
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
    <span title="Tasks and threads needing input" className="chip tone-need px-1.5 text-meta-sm leading-4">
      {n}
    </span>
  );
}

function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return target.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}

/** Mirrors the needs-input count on the installed app icon (where the Badging API exists). */
function useAppBadge(count: number | undefined) {
  useEffect(() => {
    if (count === undefined || !("setAppBadge" in navigator)) return;
    (count > 0 ? navigator.setAppBadge(count) : navigator.clearAppBadge()).catch(() => undefined);
  }, [count]);
}

function Shell() {
  const badge = useBadge().data?.needsInput;
  useAppBadge(badge);
  usePushRefresh();
  useSystemNotificationSync();
  const needsInput = badge ?? 0;
  const [inbox, setInbox] = useState(false);
  const openInbox = useCallback(() => setInbox(true), []);
  const newTask = useNewTask();
  const { theme, setTheme } = useTheme();
  const [palette, setPalette] = useState(false);
  const [more, setMore] = useState(false);
  const location = useLocation();
  const contentRef = useRef<HTMLElement>(null);
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

  const moreActive = ["/settings", "/history"].some((p) => location.pathname.startsWith(p));

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
                  "flex shrink-0 items-center gap-1.5 border-b-2 px-3 text-ui transition-colors duration-150",
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
            className="flex h-8 items-center gap-3 border border-line-strong bg-surface px-2.5 text-small whitespace-nowrap text-muted transition-colors duration-150 hover:text-fg"
          >
            <IconSearch size={14} />
            <span className="hidden lg:inline">Search tasks, threads</span>
            <Kbd>Ctrl K</Kbd>
          </button>
          <NotificationsBell onOpen={openInbox} />
          <ConnectionChip />
          <ThemeButton />
          <Button variant="primary" onClick={() => newTask()}>
            <IconPlus size={14} />
            New task
          </Button>
        </div>
      </header>

      {/* Phone top bar */}
      <header className="sticky top-0 z-40 flex h-[calc(3.25rem+env(safe-area-inset-top))] items-center gap-2 border-b border-line bg-bg pt-[env(safe-area-inset-top)] pr-[max(0.875rem,env(safe-area-inset-right))] pl-[max(0.875rem,env(safe-area-inset-left))] md:hidden">
        <Mark />
        <div className="ml-auto flex items-center gap-2">
          <ConnectionChip compact />
          <NotificationsBell onOpen={openInbox} />
          <Button variant="secondary" onClick={() => setPalette(true)} aria-label="Search" className="w-10 px-0">
            <IconSearch size={16} />
          </Button>
          <Button variant="primary" onClick={() => newTask()}>
            <IconPlus size={14} />
            Task
          </Button>
        </div>
      </header>

      <PullToReload key={location.key} contentRef={contentRef} />

      {/* Relative positioning keeps fixed task actions anchored to the viewport during a pull. */}
      <main ref={contentRef} className="relative top-[var(--pull-offset,0px)] mx-auto w-full max-w-[1240px] px-3 pt-4 pb-[calc(6rem+env(safe-area-inset-bottom))] transition-[top] duration-180 ease-out data-pulling:transition-none motion-reduce:transition-none md:px-6 md:pt-6 md:pb-10">
        <Outlet />
      </main>

      {/* Phone bottom tabs */}
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-40 grid h-[calc(3.5rem+env(safe-area-inset-bottom))] grid-cols-5 border-t border-line bg-bg pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] md:hidden"
      >
        {TABS.map((t) => (
          <NavLink
            key={t.to}
            to={t.to}
            end={t.to === "/"}
            className={({ isActive }) =>
              cx(
                "relative -mt-px flex flex-col items-center justify-center gap-1 border-t-2 text-tab",
                isActive ? "border-accent text-fg" : "border-transparent text-muted",
              )
            }
          >
            {t.icon}
            {t.label}
            {t.to === "/" && needsInput > 0 ? (
              <span className="absolute top-2 left-[calc(50%+8px)] size-2 bg-need" aria-label={`${needsInput} need input`} />
            ) : null}
          </NavLink>
        ))}
        <button
          type="button"
          onClick={() => setMore(true)}
          className={cx(
            "-mt-px flex flex-col items-center justify-center gap-1 border-t-2 text-tab",
            moreActive ? "border-accent text-fg" : "border-transparent text-muted",
          )}
        >
          <IconMore size={TAB_ICON} />
          More
        </button>
      </nav>

      {more ? (
        <Drawer title="More" onClose={() => setMore(false)}>
          <div className="flex flex-col gap-5">
            <nav className="flex flex-col border border-line" aria-label="More">
              {[{ to: "/history", label: "History" }, ...NAV.slice(4)].map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={() => setMore(false)}
                  className={({ isActive }) =>
                    cx("border-b border-line px-3 py-3 text-body last:border-b-0", isActive && "bg-panel font-medium")
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

      {inbox ? <NotificationsDrawer onClose={() => setInbox(false)} /> : <NotificationToasts onOpenInbox={openInbox} />}

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
