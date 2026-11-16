import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { useBadge, useT3Status } from "../api/queries.ts";
import { usePlatform } from "../platform/context.tsx";
import { relativeTime } from "../lib/format.ts";
import { THEMES, useTheme, type Theme } from "../lib/theme.ts";
import { CommandPalette } from "./CommandPalette.tsx";
import { HostBanner } from "./Host.tsx";
import {
  IconCheck,
  IconChevronDown,
  IconClose,
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
import { Button, Chip, Drawer, Kbd, Segmented, buttonClass, cx } from "./ui.tsx";

const NAV = [
  { to: "/", label: "Dashboard" },
  { to: "/projects", label: "Projects" },
  { to: "/sessions", label: "Sessions" },
  { to: "/schedules", label: "Schedules" },
  { to: "/history", label: "History" },
  { to: "/snippets", label: "Snippets" },
  { to: "/settings", label: "Settings" },
] as const;

/** Phone More drawer: the laptop nav items the bottom tabs leave out. */
const MORE = NAV.filter((item) => item.to === "/schedules" || item.to === "/history" || item.to === "/settings");

/** Paths of the MORE items; the laptop bar folds them into a More menu below `lg`. */
const MORE_PATHS: readonly string[] = MORE.map((item) => item.to);

/** Laptop top bar tab styling, shared by nav links and the More menu trigger. */
function navTabClass(active: boolean): string {
  return cx(
    "flex shrink-0 items-center gap-1.5 border-b-2 px-2 text-ui transition-colors duration-150 xl:px-3",
    active ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg",
  );
}

/** Laptop More menu (below `lg` only): the nav items that don't fit beside the primary tabs. */
function MoreMenu({ active }: { active: boolean }) {
  const menuId = useId();
  const menuRef = useRef<HTMLDivElement>(null);
  return (
    <>
      <button type="button" popoverTarget={menuId} className={cx(navTabClass(active), "[anchor-name:--nav-more] lg:hidden")}>
        More
        <IconChevronDown size={12} />
      </button>
      <div
        ref={menuRef}
        id={menuId}
        popover="auto"
        className="m-0 w-44 border border-line-strong bg-surface text-fg [position-anchor:--nav-more] [top:anchor(bottom)] [left:anchor(left)]"
      >
        <nav className="flex flex-col" aria-label="More">
          {MORE.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              onClick={() => menuRef.current?.hidePopover()}
              className={({ isActive }) =>
                cx("border-b border-line px-3 py-2 text-ui last:border-b-0 hover:bg-panel", isActive && "bg-panel font-medium")
              }
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </div>
    </>
  );
}

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

/** Stepped-bars glyph + wordmark; the top step turns Waiting Yellow while anything needs input. */
function Mark({ needsInput }: { needsInput: number }) {
  return (
    <Link to="/" className="flex shrink-0 items-center gap-2 font-bold tracking-[-0.02em] whitespace-nowrap">
      <svg width={16} height={16} viewBox="0 0 16 16" aria-hidden className="shrink-0 fill-current">
        <rect x={1} y={11} width={14} height={3} />
        <rect x={3} y={7} width={10} height={3} opacity={0.72} />
        <rect
          x={5}
          y={3}
          width={6}
          height={3}
          className={cx(
            "transition-[fill,opacity] duration-150 motion-reduce:transition-none",
            needsInput > 0 ? "fill-need" : "opacity-44",
          )}
        />
      </svg>
      stage
    </Link>
  );
}

function StatusRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[88px_minmax(0,1fr)] gap-2">
      <dt className="text-muted">{label}</dt>
      <dd className="truncate text-fg">{children}</dd>
    </div>
  );
}

/** Square T3 status control; links to Settings, hover/focus (pointer devices only) reveals details. */
function ConnectionStatus() {
  const status = useT3Status();
  const detailsId = useId();
  const data = status.data;
  // A data-less query returns to pending on every refetch; after the first error keep showing offline.
  const checking = status.isPending && status.errorUpdateCount === 0;
  const state = checking ? "checking" : data?.connected === true ? "connected" : "offline";
  const label = state === "checking" ? "Checking T3" : state === "connected" ? "T3 connected" : "T3 offline";
  const lastError = data?.lastError ?? (status.isError ? status.error.message : null);
  const tokenExpiresAt = data?.tokenExpiresAt ?? null;
  return (
    <div className="group relative shrink-0">
      <Link to="/settings" aria-label={label} aria-describedby={detailsId} className={cx(buttonClass("secondary"), "w-10 px-0 md:w-8")}>
        {state === "checking" ? (
          <span className="size-1.5 bg-idle" />
        ) : (
          <span className={state === "connected" ? "text-ok" : "text-bad"}>
            {state === "connected" ? <IconCheck /> : <IconClose />}
          </span>
        )}
      </Link>
      <div
        id={detailsId}
        role="tooltip"
        className="pointer-events-none invisible absolute top-full right-0 z-50 mt-1 w-[280px] border border-line-strong bg-surface opacity-0 transition-[opacity,visibility] duration-150 motion-reduce:transition-none group-hover:visible group-hover:opacity-100 [@media(hover:hover)]:group-focus-within:visible [@media(hover:hover)]:group-focus-within:opacity-100"
      >
        <div className="flex min-h-8 items-center gap-2 border-b border-line bg-panel px-3 py-1">
          <span className="label-caps text-fg">T3 connection</span>
          <Chip dot tone={state === "checking" ? "idle" : state === "connected" ? "ok" : "bad"} className="ml-auto">
            {state}
          </Chip>
        </div>
        <div className="flex flex-col gap-1 px-3 py-2 font-mono text-meta">
          <dl className="flex flex-col gap-1">
            <StatusRow label="URL">{data?.t3Url ?? "-"}</StatusRow>
            <StatusRow label="Last sync">{relativeTime(data?.lastFullSyncAt ?? null)}</StatusRow>
            <StatusRow label="Token">{tokenExpiresAt === null ? "-" : `expires ${relativeTime(tokenExpiresAt)}`}</StatusRow>
          </dl>
          {lastError === null ? null : <p className="line-clamp-2 break-words text-bad">{lastError}</p>}
        </div>
        <div className="border-t border-line px-3 py-1.5 font-mono text-meta-sm text-muted">Click to open settings</div>
      </div>
    </div>
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

/** Mirrors the needs-input count outside the page (browser: tab favicon and installed app badge). */
function useNeedsInputAttention(count: number | undefined) {
  const { attention } = usePlatform();
  useEffect(() => attention.setNeedsInput(count), [attention, count]);
}

function Shell() {
  const badge = useBadge().data?.needsInput;
  useNeedsInputAttention(badge);
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

  const moreActive = MORE_PATHS.some((p) => location.pathname.startsWith(p));

  return (
    <div className="min-h-screen bg-bg text-fg">
      {/* Laptop top bar */}
      <header className="sticky top-0 z-40 hidden h-12 items-stretch gap-4 border-b border-line bg-bg px-6 md:flex">
        <div className="flex items-center">
          <Mark needsInput={needsInput} />
        </div>
        <nav className="flex items-stretch overflow-x-auto" aria-label="Main">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === "/"}
              className={({ isActive }) => cx(navTabClass(isActive), MORE_PATHS.includes(item.to) && "max-lg:hidden")}
            >
              {item.label}
              {item.to === "/" ? <NeedsCount n={needsInput} /> : null}
            </NavLink>
          ))}
          <MoreMenu active={moreActive} />
        </nav>
        <div className="ml-auto flex items-center gap-2">
          <button
            type="button"
            onClick={() => setPalette(true)}
            aria-label="Search tasks, threads"
            className="flex h-8 items-center gap-3 border border-line-strong bg-surface px-2.5 text-small whitespace-nowrap text-muted transition-colors duration-150 hover:text-fg"
          >
            <IconSearch size={14} />
            <span className="hidden xl:inline">Search tasks, threads</span>
            <Kbd className="max-xl:hidden">Ctrl K</Kbd>
          </button>
          <NotificationsBell onOpen={openInbox} />
          <ConnectionStatus />
          <ThemeButton />
          <Button variant="primary" onClick={() => newTask()} className="max-lg:w-8 max-lg:px-0">
            <IconPlus size={14} />
            <span className="max-lg:sr-only">New task</span>
          </Button>
        </div>
      </header>

      {/* Phone top bar */}
      <header className="sticky top-0 z-40 flex h-[calc(3.25rem+env(safe-area-inset-top))] items-center gap-2 border-b border-line bg-bg pt-[env(safe-area-inset-top)] pr-[max(0.875rem,env(safe-area-inset-right))] pl-[max(0.875rem,env(safe-area-inset-left))] md:hidden">
        <Mark needsInput={needsInput} />
        <div className="ml-auto flex items-center gap-2">
          <ConnectionStatus />
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
        <HostBanner />
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
              {MORE.map((item) => (
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
