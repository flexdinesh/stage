import { useCallback, useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, matchPath, useLocation, useSearchParams } from "react-router";
import { useBadge } from "../api/queries.ts";
import { usePlatform } from "../platform/context.tsx";
import { routeLabel } from "../lib/history.ts";
import { INBOX_PARAM } from "../lib/inbox.ts";
import { useTheme, type Theme } from "../lib/theme.ts";
import { MORE_DESTINATIONS } from "../pages/MorePage.tsx";
import { CommandPalette } from "./CommandPalette.tsx";
import { HostBanner } from "./Host.tsx";
import {
  IconChevronDown,
  IconDashboard,
  IconMoon,
  IconMore,
  IconPlus,
  IconSearch,
  IconSettings,
  IconSchedules,
  IconSnippets,
  IconSun,
  IconTasks,
  IconThemeSystem,
  IconTree,
} from "./icons.tsx";
import { BackForward, NavHistoryProvider, PhoneBack, useCanGoBack } from "./NavHistory.tsx";
import { TrailSlotContext } from "./navContext.ts";
import { NewTaskProvider, useNewTask } from "./NewTask.tsx";
import { NotificationToasts, NotificationsBell, NotificationsDrawer, usePushRefresh, useSystemNotificationSync } from "./Notifications.tsx";
import { PullToReload } from "./PullToReload.tsx";
import { ScopeProvider, useScope } from "./Scope.tsx";
import { ServerMenu } from "./Servers.tsx";
import { LoadCount, ProjectNav, SideLink, useScopeLoad } from "./Sidebar.tsx";
import { StatusRow, useStatusAlert } from "./StatusRow.tsx";
import { Drawer, IconButton, Kbd, buttonClass, cx, toneClass, type IconComponent } from "./ui.tsx";

/** Phone tab bar icon size (iOS tab bars use ~22-25px glyphs). */
const TAB_ICON = 22;

/** Phone tab roots: the top bar keeps the mark there instead of Back. */
const TAB_ROOTS = ["/", "/tasks", "/trees", "/schedules", "/more"];

const TABS = [
  { to: "/", label: "Dashboard", icon: <IconDashboard size={TAB_ICON} /> },
  { to: "/tasks", label: "Tasks", icon: <IconTasks size={TAB_ICON} /> },
  { to: "/trees", label: "Trees", icon: <IconTree size={TAB_ICON} /> },
  { to: "/schedules", label: "Schedules", icon: <IconSchedules size={TAB_ICON} /> },
] as const;

function tabClass(active: boolean) {
  return cx(
    "relative -mt-px flex flex-col items-center justify-center gap-1 border-t-2 text-tab",
    active ? "border-accent text-fg" : "border-transparent text-muted",
  );
}

const THEME_ICON: Record<Theme, IconComponent> = {
  system: IconThemeSystem,
  light: IconSun,
  dark: IconMoon,
};

/** Stepped-bars glyph + wordmark; the top step turns Waiting Yellow while anything needs input. */
/** `compact` (phone top bar) keeps the wordmark for screen readers only. */
function Mark({ needsInput, compact = false }: { needsInput: number; compact?: boolean }) {
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
      <span className={cx(compact && "sr-only")}>stage</span>
    </Link>
  );
}

function ThemeButton() {
  const { theme, cycle } = useTheme();
  return (
    <IconButton variant="secondary" onClick={cycle} title={`Theme: ${theme} (click to change)`} label={`Theme: ${theme}`} icon={THEME_ICON[theme]} />
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
  const [palette, setPalette] = useState(false);
  const location = useLocation();
  const [params, setParams] = useSearchParams();
  const inboxRequested = params.has(INBOX_PARAM);
  const contentRef = useRef<HTMLElement>(null);
  const closePalette = useCallback(() => setPalette(false), []);
  const openNewTask = useCallback(() => newTask(), [newTask]);

  // `?inbox` (desktop summary notification click, cold start or an existing route) opens the drawer
  // when it appears; the effect then drops the param so a reload or Back does not reopen it.
  const [inboxRequestSeen, setInboxRequestSeen] = useState(false);
  if (inboxRequested !== inboxRequestSeen) {
    setInboxRequestSeen(inboxRequested);
    if (inboxRequested) setInbox(true);
  }
  useEffect(() => {
    if (!inboxRequested) return;
    setParams(
      (current) => {
        const next = new URLSearchParams(current);
        next.delete(INBOX_PARAM);
        return next;
      },
      { replace: true },
    );
  }, [inboxRequested, setParams]);

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

  const onMorePage = matchPath("/more", location.pathname) !== null;
  const inMoreSection = MORE_DESTINATIONS.some(({ to }) => matchPath({ path: to, end: false }, location.pathname) !== null);
  const scopeLoad = useScopeLoad();
  const scope = useScope();
  const [scopeSheet, setScopeSheet] = useState(false);
  const [trailSlot, setTrailSlot] = useState<HTMLElement | null>(null);
  const canGoBack = useCanGoBack();
  const showBack = canGoBack && !TAB_ROOTS.includes(location.pathname);
  const statusAlert = useStatusAlert();

  return (
    <div className="min-h-screen bg-bg text-fg lg:grid lg:grid-cols-[248px_minmax(0,1fr)]">
      {/* Laptop sidebar */}
      <aside className="sticky top-0 z-40 hidden h-screen min-w-0 flex-col border-r border-line bg-bg lg:flex">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line pr-3 pl-4">
          <Mark needsInput={needsInput} />
          <div className="ml-auto flex items-center gap-1.5">
            <ThemeButton />
            <NotificationsBell onOpen={openInbox} />
          </div>
        </div>
        <div className="flex shrink-0 gap-2 px-3 pt-3">
          <button
            type="button"
            onClick={() => setPalette(true)}
            aria-label="Search tasks, projects"
            className="flex h-8 min-w-0 flex-1 items-center gap-2 border border-line-strong bg-surface px-2.5 text-small whitespace-nowrap text-muted transition-colors duration-150 hover:text-fg"
          >
            <IconSearch size={14} />
            Search
            <Kbd className="ml-auto">Ctrl K</Kbd>
          </button>
          <IconButton variant="primary" onClick={() => newTask()} title="New task (N)" label="New task" icon={IconPlus} />
        </div>
        <nav aria-label="Main" className="flex shrink-0 flex-col px-2 pt-3">
          <SideLink to="/" icon={<IconDashboard />} label="Dashboard" count={<LoadCount load={scopeLoad} />} />
          <SideLink to="/tasks" icon={<IconTasks />} label="Tasks" />
          <SideLink to="/trees" icon={<IconTree />} label="Trees" />
          <SideLink to="/schedules" icon={<IconSchedules />} label="Schedules" />
        </nav>
        <div className="min-h-0 flex-1 overflow-y-auto px-2 pt-1 pb-3">
          <ProjectNav />
        </div>
        <div className="flex shrink-0 flex-col border-t border-line px-2 py-2">
          <SideLink to="/snippets" icon={<IconSnippets />} label="Snippets" />
          <SideLink to="/settings" icon={<IconSettings />} label="Settings" />
          <ServerMenu className="mx-0.5 mt-2" />
          <StatusRow className="mt-2 border-t border-line pt-2" />
        </div>
      </aside>

      <div className="min-w-0 pt-[calc(3.25rem+var(--inset-top))] lg:pt-0">
      {/* Phone top bar: fixed, not sticky (iOS WebKit can rasterize a sticky bar blurry after a PWA reload); --inset-top clears the iOS 26 PWA edge blur. */}
      <header className="fixed inset-x-0 top-0 z-40 flex h-[calc(3.25rem+var(--inset-top))] items-center gap-2 border-b border-line bg-bg pt-[var(--inset-top)] pr-[max(0.875rem,env(safe-area-inset-right))] pl-[max(0.875rem,env(safe-area-inset-left))] lg:hidden">
        {/* Fixed slot: Back and the mark swap without moving the rest of the bar. */}
        <div className="flex w-10 shrink-0 items-center">
          {showBack ? <PhoneBack /> : <Mark needsInput={needsInput} compact />}
        </div>
        <button
          type="button"
          onClick={() => setScopeSheet(true)}
          aria-label={`Project: ${scope.project?.title ?? "All projects"}`}
          className={cx(buttonClass("secondary"), "h-10 min-w-0 gap-1.5 px-2.5 font-mono text-meta font-normal")}
        >
          <span className="min-w-0 truncate">{scope.project?.title ?? "All projects"}</span>
          <IconChevronDown size={12} />
        </button>
        <div className="ml-auto flex shrink-0 items-center gap-2">
          <NotificationsBell onOpen={openInbox} />
          <ServerMenu />
          <IconButton variant="secondary" onClick={() => setPalette(true)} label="Search" icon={IconSearch} />
          <IconButton variant="primary" onClick={() => newTask()} label="New task" icon={IconPlus} />
        </div>
      </header>

      {/* Laptop content bar: ruled level with the sidebar header; arrows and the page's location (PageHeader trail). */}
      <div className="sticky top-0 z-30 hidden h-12 border-b border-line bg-bg lg:block">
        <div className="group/bar mx-auto flex h-full w-full max-w-[1240px] items-center gap-3 px-6">
          <BackForward />
          <nav aria-label="Location" className="min-w-0 truncate font-mono text-detail text-muted">
            <span className="text-fg group-has-[[data-trail]>*]/bar:hidden">{routeLabel(location.pathname)}</span>
            <span data-trail ref={setTrailSlot} className="contents" />
          </nav>
        </div>
      </div>

      <PullToReload key={location.key} contentRef={contentRef} />

      {/* Relative positioning keeps fixed task actions anchored to the viewport during a pull. */}
      <main ref={contentRef} className="relative top-[var(--pull-offset,0px)] mx-auto w-full max-w-[1240px] px-3 pt-4 pb-[calc(6rem+env(safe-area-inset-bottom))] transition-[top] duration-180 ease-out data-pulling:transition-none motion-reduce:transition-none md:px-6 md:pt-6 lg:pb-10">
        <HostBanner />
        <TrailSlotContext.Provider value={trailSlot}>
          <Outlet />
        </TrailSlotContext.Provider>
      </main>

      {/* Phone bottom tabs */}
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-40 grid h-[calc(3.5rem+env(safe-area-inset-bottom))] grid-cols-5 border-t border-line bg-bg pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] lg:hidden"
      >
        {TABS.map((t) => (
          <NavLink
            key={t.to}
            to={t.to}
            end={t.to === "/"}
            className={({ isActive }) => tabClass(isActive)}
          >
            {t.icon}
            {t.label}
            {t.to === "/" && needsInput > 0 ? (
              <span className="absolute top-2 left-[calc(50%+8px)] size-2 bg-need" aria-label={`${needsInput} need input`} />
            ) : null}
          </NavLink>
        ))}
        <Link
          to="/more"
          aria-current={onMorePage ? "page" : inMoreSection ? "true" : undefined}
          className={tabClass(onMorePage || inMoreSection)}
        >
          <IconMore size={TAB_ICON} />
          More
          {statusAlert === null ? null : (
            <span className={cx("absolute top-2 left-[calc(50%+8px)] size-2 bg-(--c)", toneClass(statusAlert))} aria-label="Connection needs attention" />
          )}
        </Link>
      </nav>

      </div>

      {scopeSheet ? (
        <Drawer title="Projects" onClose={() => setScopeSheet(false)}>
          <ProjectNav touch onPick={() => setScopeSheet(false)} />
        </Drawer>
      ) : null}

      {inbox ? <NotificationsDrawer onClose={() => setInbox(false)} /> : <NotificationToasts onOpenInbox={openInbox} />}

      {palette ? <CommandPalette onClose={closePalette} onNewTask={openNewTask} /> : null}
    </div>
  );
}

export function Layout() {
  return (
    <NavHistoryProvider>
      <ScopeProvider>
        <NewTaskProvider>
          <Shell />
        </NewTaskProvider>
      </ScopeProvider>
    </NavHistoryProvider>
  );
}
