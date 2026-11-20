import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Link, NavLink, Outlet, matchPath, useLocation, useSearchParams } from "react-router";
import { useBadge, useHarnesses, useOpenCodeStatus, useT3Status } from "../api/queries.ts";
import { usePlatform } from "../platform/context.tsx";
import { relativeTime } from "../lib/format.ts";
import { INBOX_PARAM } from "../lib/inbox.ts";
import { useTheme, type Theme } from "../lib/theme.ts";
import { t3Identity } from "../lib/onboarding.ts";
import {
  openCodeErrorText,
  openCodeSummary,
  overallStatus,
  t3ErrorText,
  t3Summary,
  type HarnessSummary,
  type HarnessTone,
  type OverallStatus,
  type StatusRead,
} from "../lib/connections.ts";
import { serviceHostText, signInText } from "../lib/opencode.ts";
import { MORE_DESTINATIONS } from "../pages/MorePage.tsx";
import { CommandPalette } from "./CommandPalette.tsx";
import { HostBanner } from "./Host.tsx";
import {
  IconChainLink,
  IconCheck,
  IconChevronDown,
  IconClose,
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
} from "./icons.tsx";
import { NewTaskProvider, useNewTask } from "./NewTask.tsx";
import { NotificationToasts, NotificationsBell, NotificationsDrawer, usePushRefresh, useSystemNotificationSync } from "./Notifications.tsx";
import { PullToReload } from "./PullToReload.tsx";
import { ScopeProvider, useScope } from "./Scope.tsx";
import { ServerMenu } from "./Servers.tsx";
import { AttentionCount, ProjectNav, SideLink, useScopeAttention } from "./Sidebar.tsx";
import { Button, Chip, Drawer, Kbd, buttonClass, cx } from "./ui.tsx";

/** Phone tab bar icon size (iOS tab bars use ~22-25px glyphs). */
const TAB_ICON = 22;

const TABS = [
  { to: "/", label: "Dashboard", icon: <IconDashboard size={TAB_ICON} /> },
  { to: "/tasks", label: "Tasks", icon: <IconTasks size={TAB_ICON} /> },
  { to: "/schedules", label: "Schedules", icon: <IconSchedules size={TAB_ICON} /> },
] as const;

function tabClass(active: boolean) {
  return cx(
    "relative -mt-px flex flex-col items-center justify-center gap-1 border-t-2 text-tab",
    active ? "border-accent text-fg" : "border-transparent text-muted",
  );
}

const THEME_ICON: Record<Theme, ReactNode> = {
  system: <IconThemeSystem />,
  light: <IconSun />,
  dark: <IconMoon />,
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

function StatusRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid grid-cols-[72px_minmax(0,1fr)] gap-2">
      <dt className="text-muted">{label}</dt>
      <dd className="truncate text-fg">{children}</dd>
    </div>
  );
}

/** A status query as the control reads it; a data-less query returns to pending on every refetch, so only the first counts as checking. */
function statusRead<T>(q: { data: T | undefined; isPending: boolean; errorUpdateCount: number; error: Error | null }): StatusRead<T> {
  return { data: q.data, checking: q.isPending && q.errorUpdateCount === 0, error: q.error?.message ?? null };
}

const TONE_TEXT: Record<HarnessTone, string> = { ok: "text-ok", need: "text-need-fg", bad: "text-bad", idle: "text-muted" };

function StatusIcon({ tone }: { tone: OverallStatus["tone"] }) {
  switch (tone) {
    case "none":
      return <IconChainLink size={16} />;
    case "idle":
      return <span className="size-1.5 bg-idle" />;
    case "ok":
      return <span className={TONE_TEXT.ok}><IconCheck /></span>;
    case "need":
      return <span className={TONE_TEXT.need}><IconChainLink size={16} /></span>;
    case "bad":
      return <span className={TONE_TEXT.bad}><IconClose /></span>;
  }
}

function StatusSection({ title, summary, error, children }: { title: string; summary: HarnessSummary; error: string | null; children: ReactNode }) {
  return (
    <section className="border-b border-line">
      <div className="flex min-h-8 items-center gap-2 border-b border-line bg-panel px-3 py-1">
        <span className="label-caps text-fg">{title}</span>
        <Chip dot tone={summary.tone} className="ml-auto">
          {summary.state}
        </Chip>
      </div>
      <div className="flex flex-col gap-1 px-3 py-2 font-mono text-meta">
        <dl className="flex flex-col gap-1">{children}</dl>
        {error === null ? null : <p className="line-clamp-2 break-words text-bad">{error}</p>}
      </div>
    </section>
  );
}

/** Square harness status control; links to Settings, hover/focus (pointer devices only) reveals one section per configured harness. */
function ConnectionStatus() {
  const detailsId = useId();
  const harnesses = useHarnesses();
  const t3 = statusRead(useT3Status());
  const openCode = statusRead(useOpenCodeStatus());
  // Until connections load, keep checking; if they never load, fall back to T3 alone.
  const showT3 = harnesses.loaded ? harnesses.activeConnection("t3")?.configured === true : harnesses.error != null;
  const showOpenCode = harnesses.loaded && harnesses.activeConnection("opencode")?.configured === true;
  const t3Status = showT3 ? t3Summary(t3) : null;
  const openCodeStatus = showOpenCode ? openCodeSummary(openCode) : null;
  const summaries = [t3Status, openCodeStatus].filter((s) => s !== null);
  const overall: OverallStatus = harnesses.loaded || showT3 ? overallStatus(summaries) : { tone: "idle", label: "Checking harnesses" };
  const t3Data = t3.data;
  const openCodeData = openCode.data;
  const tokenExpiresAt = t3Data?.tokenExpiresAt ?? null;
  return (
    <div className="group relative shrink-0">
      <Link to="/settings" aria-label={overall.label} aria-describedby={detailsId} className={cx(buttonClass("secondary"), "w-10 px-0 md:w-8")}>
        <StatusIcon tone={overall.tone} />
      </Link>
      <div
        id={detailsId}
        role="tooltip"
        className="pointer-events-none invisible absolute top-full right-0 z-50 mt-1 w-[280px] lg:right-auto lg:left-0 border border-line-strong bg-surface opacity-0 transition-[opacity,visibility] duration-150 motion-reduce:transition-none group-hover:visible group-hover:opacity-100 [@media(hover:hover)]:group-focus-within:visible [@media(hover:hover)]:group-focus-within:opacity-100"
      >
        {t3Status === null ? null : (
          <StatusSection title="T3" summary={t3Status} error={t3ErrorText(t3)}>
            <StatusRow label="T3">{(t3Data === undefined ? null : t3Identity(t3Data)) ?? "-"}</StatusRow>
            <StatusRow label="URL">{t3Data?.t3Url ?? "-"}</StatusRow>
            <StatusRow label="Last sync">{relativeTime(t3Data?.lastFullSyncAt ?? null)}</StatusRow>
            <StatusRow label="Token">{tokenExpiresAt === null ? "-" : `${t3Data?.state === "expired" ? "expired" : "expires"} ${relativeTime(tokenExpiresAt)}`}</StatusRow>
          </StatusSection>
        )}
        {openCodeStatus === null ? null : (
          <StatusSection title="OpenCode" summary={openCodeStatus} error={openCodeErrorText(openCode)}>
            <StatusRow label="Service">{openCodeData === undefined || openCodeData.endpoint === "" ? "-" : serviceHostText(openCodeData)}</StatusRow>
            <StatusRow label="Sign-in">{openCodeData === undefined ? "-" : signInText(openCodeData)}</StatusRow>
            <StatusRow label="Checked">{relativeTime(openCodeData?.checkedAt ?? null)}</StatusRow>
          </StatusSection>
        )}
        {summaries.length > 0 ? null : (
          <div className="flex min-h-8 items-center gap-2 border-b border-line bg-panel px-3 py-1">
            <span className="label-caps text-fg">Harnesses</span>
            <Chip dot tone="idle" className="ml-auto">
              {overall.tone === "none" ? "not connected" : "checking"}
            </Chip>
          </div>
        )}
        <div className="px-3 py-1.5 font-mono text-meta-sm text-muted">Click to open settings</div>
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
  const attention = useScopeAttention();
  const scope = useScope();
  const [scopeSheet, setScopeSheet] = useState(false);

  return (
    <div className="min-h-screen bg-bg text-fg lg:grid lg:grid-cols-[248px_minmax(0,1fr)]">
      {/* Laptop sidebar */}
      <aside className="sticky top-0 z-40 hidden h-screen min-w-0 flex-col border-r border-line bg-bg lg:flex">
        <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line pr-3 pl-4">
          <Mark needsInput={needsInput} />
          <div className="ml-auto flex items-center gap-1.5">
            <ThemeButton />
            <NotificationsBell onOpen={openInbox} />
            <ConnectionStatus />
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
          <Button variant="primary" onClick={() => newTask()} title="New task (N)" aria-label="New task" className="w-8 shrink-0 px-0">
            <IconPlus size={14} />
          </Button>
        </div>
        <nav aria-label="Main" className="flex shrink-0 flex-col px-2 pt-3">
          <SideLink to="/" icon={<IconDashboard />} label="Dashboard" count={<AttentionCount need={attention.need} total={attention.total} />} />
          <SideLink to="/tasks" icon={<IconTasks />} label="Tasks" />
          <SideLink to="/schedules" icon={<IconSchedules />} label="Schedules" />
        </nav>
        <div className="min-h-0 flex-1 overflow-y-auto px-2 pt-1 pb-3">
          <ProjectNav />
        </div>
        <div className="flex shrink-0 flex-col border-t border-line px-2 py-2">
          <SideLink to="/snippets" icon={<IconSnippets />} label="Snippets" />
          <SideLink to="/settings" icon={<IconSettings />} label="Settings" />
          <ServerMenu className="mx-0.5 mt-2" />
        </div>
      </aside>

      <div className="min-w-0">
      {/* Phone top bar */}
      <header className="sticky top-0 z-40 flex h-[calc(3.25rem+env(safe-area-inset-top))] items-center gap-2 border-b border-line bg-bg pt-[env(safe-area-inset-top)] pr-[max(0.875rem,env(safe-area-inset-right))] pl-[max(0.875rem,env(safe-area-inset-left))] lg:hidden">
        <Mark needsInput={needsInput} compact />
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
          <ConnectionStatus />
          <NotificationsBell onOpen={openInbox} />
          <ServerMenu />
          <Button variant="secondary" onClick={() => setPalette(true)} aria-label="Search" className="w-10 shrink-0 px-0">
            <IconSearch size={16} />
          </Button>
          <Button variant="primary" onClick={() => newTask()} aria-label="New task" className="w-10 shrink-0 px-0">
            <IconPlus size={16} />
          </Button>
        </div>
      </header>

      <PullToReload key={location.key} contentRef={contentRef} />

      {/* Relative positioning keeps fixed task actions anchored to the viewport during a pull. */}
      <main ref={contentRef} className="relative top-[var(--pull-offset,0px)] mx-auto w-full max-w-[1240px] px-3 pt-4 pb-[calc(6rem+env(safe-area-inset-bottom))] transition-[top] duration-180 ease-out data-pulling:transition-none motion-reduce:transition-none md:px-6 md:pt-6 lg:pb-10">
        <HostBanner />
        <Outlet />
      </main>

      {/* Phone bottom tabs */}
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-40 grid h-[calc(3.5rem+env(safe-area-inset-bottom))] grid-cols-4 border-t border-line bg-bg pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] lg:hidden"
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
    <ScopeProvider>
      <NewTaskProvider>
        <Shell />
      </NewTaskProvider>
    </ScopeProvider>
  );
}
